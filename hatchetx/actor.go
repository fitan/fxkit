package hatchetx

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/hatchet-dev/hatchet/pkg/client/types"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

// DefaultActorConcurrencyExpr 以 actor 实例 id 作为 Hatchet 并发键。
// 输入类型必须直接匿名嵌入 [ActorRef]，从而把顶层 "actorId" 序列化出来。
const DefaultActorConcurrencyExpr = "input.actorId"

// ActorRef 必须被匿名嵌入到 actor 命令载荷中，使并发 Expression "input.actorId" 生效。
// 嵌入时不得再给它 json 名字（例如 `json:"ref"`），否则 actorId 会被嵌套。
type ActorRef struct {
	ActorID string `json:"actorId"`
}

// GetActorID 返回 actor id，便于业务代码读取。
func (a ActorRef) GetActorID() string { return a.ActorID }

// ActorRefValue 让匿名嵌入 ActorRef 的载荷自动满足 [ActorInput]。
func (a ActorRef) ActorRefValue() ActorRef { return a }

// ActorInput 是 actor 命令载荷的强类型约束。
//
// 它要求载荷提供 ActorRef，从而在编译期排除 map、raw JSON、任意字段等
// 无法保证顶层 actorId 的输入；NewActor 还会在启动期校验 JSON 形状。
type ActorInput interface {
	ActorRefValue() ActorRef
}

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
	// extract 从已约束的 ActorInput 中提取 actorId；NewActor 注入。
	extract func(In) string
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
// 空字符串会保持默认值：表达式为空时 Hatchet 会让整个 workflow 共享一个并发组，
// 在调用方意外传空时静默地退化为全局串行。
//
// 即使使用自定义表达式，输入类型仍需嵌入 [ActorRef] 以满足 [ActorInput]；
// actorId 的值是否必填由 [WithActorIDRequired] 决定。
func WithActorConcurrencyExpr(expr string) ActorOption {
	return func(o *actorOptions) {
		if s := strings.TrimSpace(expr); s != "" {
			o.concurrencyExpr = s
		}
	}
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
// 输入类型必须直接匿名嵌入 [ActorRef]（不能是指针、不能是命名嵌入、不能是
// 间接嵌入），且自定义 JSON 序列化不得改变顶层 "actorId"。校验失败时返回
// 错误，宿主在装配期失败，避免带病启动。
func NewActor[In ActorInput, Out any](name string, handler ActorHandler[In, Out], opts ...ActorOption) (*Actor[In, Out], error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("hatchetx: actor name is required")
	}
	if handler == nil {
		return nil, fmt.Errorf("hatchetx: actor %q handler is nil", name)
	}
	if err := validateActorInput[In](); err != nil {
		return nil, err
	}
	out := actorOptions{
		concurrencyExpr: DefaultActorConcurrencyExpr,
		maxRuns:         1,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&out)
		}
	}
	// 默认并发表达式就是 input.actorId，因此必须校验；
	// 自定义表达式时默认不校验，除非显式 WithActorIDRequired()。
	if !out.actorIDRequired && (out.concurrencyExpr == "" || out.concurrencyExpr == DefaultActorConcurrencyExpr) {
		out.actorIDRequired = true
	}
	if out.description == "" {
		out.description = fmt.Sprintf("semantic actor %q (concurrency mailbox by actorId)", name)
	}

	inner := handler
	handler = func(ctx context.Context, in In) (Out, error) {
		start := time.Now()
		res, err := inner(ctx, in)
		recordActorRun(ctx, name, time.Since(start), err != nil)
		return res, err
	}

	return &Actor[In, Out]{
		name:    name,
		handler: handler,
		opts:    out,
		extract: func(in In) string { return in.ActorRefValue().ActorID },
	}, nil
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

const actorIDCheckValue = "fxkit-actor-id-check"

// validateActorInput 在启动期校验 actor 输入类型：
//   - 必须直接匿名嵌入 [ActorRef]（不能是指针、命名嵌入或间接嵌入）；
//   - 嵌入字段不能有 json 名字；
//   - 序列化结果必须在顶层包含 actorId（拦截自定义 MarshalJSON 改变形状）。
func validateActorInput[In ActorInput]() error {
	t := reflect.TypeFor[In]()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("hatchetx: actor input %s must be a struct embedding hatchetx.ActorRef", t)
	}

	refType := reflect.TypeFor[ActorRef]()
	idx := -1
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.Anonymous || f.Type != refType {
			continue
		}
		if name := embeddedJSONName(f.Tag); name != "" {
			return fmt.Errorf("hatchetx: actor input %s: embedded ActorRef must not have a json name (got %q)", t, name)
		}
		idx = i
		break
	}
	if idx < 0 {
		return fmt.Errorf("hatchetx: actor input %s must embed hatchetx.ActorRef as an anonymous value field", t)
	}

	sample := reflect.New(t)
	field := sample.Elem().Field(idx)
	if !field.CanSet() {
		return fmt.Errorf("hatchetx: actor input %s: embedded ActorRef is not settable", t)
	}
	field.Set(reflect.ValueOf(ActorRef{ActorID: actorIDCheckValue}))
	raw, err := json.Marshal(sample.Interface())
	if err != nil {
		return fmt.Errorf("hatchetx: actor input %s: marshal: %w", t, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("hatchetx: actor input %s must JSON-serialize to an object: %w", t, err)
	}
	if got, _ := m["actorId"].(string); got != actorIDCheckValue {
		return fmt.Errorf("hatchetx: actor input %s must JSON-serialize top-level \"actorId\" (embed ActorRef without a json name; custom MarshalJSON is not allowed to change it)", t)
	}
	return nil
}

// embeddedJSONName 返回 json tag 中显式指定的名字；`json:",omitempty"` 返回空。
func embeddedJSONName(tag reflect.StructTag) string {
	t := strings.TrimSpace(tag.Get("json"))
	if t == "" {
		return ""
	}
	if i := strings.IndexByte(t, ','); i >= 0 {
		t = t[:i]
	}
	return t
}

func (a *Actor[In, Out]) extractActorID(in In) string {
	if a == nil || a.extract == nil {
		return ""
	}
	return strings.TrimSpace(a.extract(in))
}

func (a *Actor[In, Out]) validateActorID(in In) error {
	if !a.requiresActorID() {
		return nil
	}
	if a.extractActorID(in) == "" {
		return fmt.Errorf("hatchetx: actorId is required (embed hatchetx.ActorRef and set ActorID)")
	}
	return nil
}

// Call 把 actor 命令入队并等待结果（按 actorId 串行）。
//
// 注意：ctx 只覆盖入队阶段。Hatchet Go SDK 的 Result() 不接收 context，
// 运行卡住时 Call 会一直等到引擎侧超时；需要硬超时请在调用方用 goroutine
// 包装并在 ctx.Done() 时放弃等待（会泄漏一个等待 goroutine）。
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
