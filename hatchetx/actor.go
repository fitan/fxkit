package hatchetx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/hatchet-dev/hatchet/pkg/client/types"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

// DefaultActorConcurrencyExpr 以 actor 实例 id 作为 Hatchet 并发键。
// 输入载荷必须把 "actorId" 字段 JSON 序列化出来（见 [ActorRef]）。
const DefaultActorConcurrencyExpr = "input.actorId"

// ActorRef 嵌入到命令载荷中，使并发 Expression "input.actorId" 生效。
// 优先嵌入本类型，而不是只实现裸 GetActorID()——Call/CallAsync 要求
// 序列化后的 JSON 包含 "actorId"。
type ActorRef struct {
	ActorID string `json:"actorId"`
}

// GetActorID 实现 [Actor.Call] 使用的 actor-id 约定。
func (a ActorRef) GetActorID() string { return a.ActorID }

// ActorHandler 是单个 actor 命令的业务处理器。
// Hatchet 并发（MaxRuns=1）保证同一 actorId 内串行。
// 处理器应在外部加载/存储持久状态；worker 进程不具备粘性。
type ActorHandler[In, Out any] func(ctx context.Context, in In) (Out, error)

// Actor 是 Hatchet 上带类型的并发邮箱（语义 actor）：
// 每个 actorId 一个队列，以 MaxRuns=1 和 GROUP_ROUND_ROBIN 处理。
// 它不提供粘性 worker，也不提供内存内 actor 激活。
type Actor[In, Out any] struct {
	name    string
	handler ActorHandler[In, Out]
	opts    actorOptions
}

type actorOptions struct {
	description     string
	concurrencyExpr string
	maxRuns         int32
	// actorIDRequired 为 true 时，Call/CallAsync 强制校验输入里有 actorId。
	//
	// 默认：使用内置并发表达式时为 true；用 WithActorConcurrencyExpr 覆盖后为 false
	// （因为那时并发键可能不是 actorId）。需要保留校验时显式加 WithActorIDRequired()。
	actorIDRequired bool
}

// ActorOption 配置 [NewActor]。
type ActorOption func(*actorOptions)

// WithActorDescription 设置 Hatchet workflow 描述。
func WithActorDescription(desc string) ActorOption {
	return func(o *actorOptions) { o.description = desc }
}

// WithActorConcurrencyExpr 覆盖 CEL 并发键（默认：input.actorId）。
func WithActorConcurrencyExpr(expr string) ActorOption {
	return func(o *actorOptions) { o.concurrencyExpr = expr }
}

// WithActorMaxRuns 覆盖并发 group 的 MaxRuns（默认：1）。
func WithActorMaxRuns(n int32) ActorOption {
	return func(o *actorOptions) {
		if n > 0 {
			o.maxRuns = n
		}
	}
}

// WithActorIDRequired 要求输入必须携带 actorId（即使自定义了并发表达式）。
//
// 早期实现用 strings.Contains(expr, "actorId") 猜：表达式叫
// "input.tenant_actorId" 会误判为需要，写成 input["actorId"] 又会漏判。
// 现在改为显式声明。
func WithActorIDRequired() ActorOption {
	return func(o *actorOptions) { o.actorIDRequired = true }
}

// NewActor 定义一个由 Hatchet standalone task 承载的语义 actor 命令。
//
// In 必须把 "actorId" JSON 序列化出来，以配合默认的并发 CEL（嵌入
// [ActorRef]，或使用带 json:"actorId" 的导出字段）。只有 GetActorID() 方法
// 是不够的——Hatchet 基于 JSON 输入而非 Go 方法计算并发。
// 若用 [WithActorConcurrencyExpr] 覆盖 CEL，仍需在 JSON 中保留 actorId，
// 除非自定义表达式改用另一个你能保证存在的输入字段。
func NewActor[In, Out any](name string, handler ActorHandler[In, Out], opts ...ActorOption) *Actor[In, Out] {
	name = strings.TrimSpace(name)
	o := actorOptions{
		concurrencyExpr: DefaultActorConcurrencyExpr,
		maxRuns:         1,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	// 默认并发表达式就是 input.actorId，因此必须校验；
	// 自定义表达式时默认不校验，除非显式 WithActorIDRequired()。
	//
	// 注意：显式选项优先，所以只在用户没加过该选项时才按表达式推导。
	if !o.actorIDRequired && (o.concurrencyExpr == "" || o.concurrencyExpr == DefaultActorConcurrencyExpr) {
		o.actorIDRequired = true
	}
	if o.description == "" {
		o.description = fmt.Sprintf("semantic actor %q (concurrency mailbox by actorId)", name)
	}

	inType := reflect.TypeFor[In]()
	if o.concurrencyExpr == DefaultActorConcurrencyExpr &&
		typeHasActorIDField(inType) && !typeHasJSONActorID(inType) {
		// 初始化时提前告警：输入类型带 actor ID 但缺少 json:"actorId"
		slog.Warn("hatchetx: NewActor: input type has actor ID but does not JSON-serialize 'actorId' (embed hatchetx.ActorRef or field with json:\"actorId\")",
			"actor", name, "type", inType.String())
	}
	// 自定义表达式里看起来引用了 actorId，但没声明 WithActorIDRequired()：
	// 只告警，不改变行为（早期是自动开启，但字符串推断会误判）。
	if !o.actorIDRequired && strings.Contains(o.concurrencyExpr, "actorId") {
		slog.Warn("hatchetx: NewActor: concurrency expression references actorId but actor-ID validation is off; add WithActorIDRequired() if the input must carry it",
			"actor", name, "expr", o.concurrencyExpr)
	}

	if handler != nil {
		inner := handler
		handler = func(ctx context.Context, in In) (Out, error) {
			start := time.Now()
			out, err := inner(ctx, in)
			recordActorRun(ctx, name, time.Since(start), err != nil)
			return out, err
		}
	}

	return &Actor[In, Out]{name: name, handler: handler, opts: o}
}

// Name 返回 Hatchet workflow / task 名称。
func (a *Actor[In, Out]) Name() string {
	if a == nil {
		return ""
	}
	return a.name
}

// Registrar 返回把该 actor 注册到共享 worker 的 [Registrar]。
func (a *Actor[In, Out]) Registrar() Registrar {
	return func(client *Client) ([]WorkflowBase, error) {
		return a.Register(client)
	}
}

// Register 构建带 per-actorId 并发的 Hatchet standalone task。
func (a *Actor[In, Out]) Register(client *Client) ([]WorkflowBase, error) {
	if a == nil || a.handler == nil {
		return nil, fmt.Errorf("hatchetx: actor is nil")
	}
	if client == nil || !client.Enabled() {
		return nil, nil
	}
	name := strings.TrimSpace(a.name)
	if name == "" {
		return nil, fmt.Errorf("hatchetx: actor name is required")
	}

	maxRuns := a.opts.maxRuns
	strategy := types.GroupRoundRobin
	handler := a.handler
	task := client.SDK.NewStandaloneTask(
		name,
		func(ctx hatchet.Context, in In) (Out, error) {
			return handler(ctx, in)
		},
		hatchet.WithWorkflowDescription(a.opts.description),
		hatchet.WithWorkflowConcurrency(types.Concurrency{
			Expression:    a.opts.concurrencyExpr,
			MaxRuns:       &maxRuns,
			LimitStrategy: &strategy,
		}),
	)
	return []WorkflowBase{task}, nil
}

func (a *Actor[In, Out]) requiresActorID() bool {
	if a == nil {
		return true
	}
	return a.opts.actorIDRequired
}

func (a *Actor[In, Out]) extractActorID(in In) string {
	return actorIDOf(in)
}

func (a *Actor[In, Out]) validateActorID(in In) error {
	id := a.extractActorID(in)
	if id == "" {
		return fmt.Errorf("hatchetx: actorId is required (embed hatchetx.ActorRef or field with json:\\\"actorId\\\")")
	}
	// 快速路径：若该类型静态含有标记为 json:"actorId" 的字段，
	// 且未自定义序列化，则 json.Marshal 必然输出 "actorId": id。
	// 此时跳过冗余的预序列化，交由 SDK Run 完成唯一的序列化。
	if typeHasJSONActorID(reflect.TypeFor[In]()) {
		return nil
	}
	return assertActorIDInJSON(in, id)
}

// Call 把 actor 命令入队并等待结果（按 actorId 串行）。
func (a *Actor[In, Out]) Call(ctx context.Context, client *Client, in In, opts ...hatchet.RunOptFunc) (Out, error) {
	var zero Out
	if a == nil {
		return zero, fmt.Errorf("hatchetx: actor is nil")
	}
	name := strings.TrimSpace(a.name)
	if name == "" {
		return zero, fmt.Errorf("hatchetx: actor name is required")
	}
	if client == nil || !client.Enabled() {
		return zero, fmt.Errorf("hatchetx: client disabled")
	}
	if a.requiresActorID() {
		if err := a.validateActorID(in); err != nil {
			return zero, err
		}
	} else {
		if _, err := json.Marshal(in); err != nil {
			return zero, fmt.Errorf("hatchetx: marshal actor input: %w", err)
		}
	}
	result, err := client.SDK.Run(ctx, name, in, opts...)
	if err != nil {
		return zero, fmt.Errorf("hatchetx: actor %q call: %w", name, err)
	}
	if result == nil {
		return zero, fmt.Errorf("hatchetx: actor %q returned nil result", name)
	}
	if err := result.TaskOutput(name).Into(&zero); err != nil {
		return zero, fmt.Errorf("hatchetx: actor %q result: %w", name, err)
	}
	return zero, nil
}

// CallAsync 触发 actor 命令且不等待，返回 Hatchet run id。
func (a *Actor[In, Out]) CallAsync(ctx context.Context, client *Client, in In, opts ...hatchet.RunOptFunc) (string, error) {
	if a == nil {
		return "", fmt.Errorf("hatchetx: actor is nil")
	}
	name := strings.TrimSpace(a.name)
	if name == "" {
		return "", fmt.Errorf("hatchetx: actor name is required")
	}
	if client == nil || !client.Enabled() {
		return "", fmt.Errorf("hatchetx: client disabled")
	}
	if a.requiresActorID() {
		if err := a.validateActorID(in); err != nil {
			return "", err
		}
	} else {
		if _, err := json.Marshal(in); err != nil {
			return "", fmt.Errorf("hatchetx: marshal actor input: %w", err)
		}
	}
	return client.RunNoWait(ctx, name, in, opts...)
}

// ProvideActor 把该 actor 的 [Registrar] 发布到 hatchet_registrars fx group。
// 当处理器需要 Fx 依赖时，优先在 registrar 工厂内构造 Actor。
func ProvideActor[In, Out any](actor *Actor[In, Out]) di.Option {
	if actor == nil {
		return nil
	}
	return ProvideRegistrar(actor.Registrar())
}
