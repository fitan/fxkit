// Package hatchetx 把 Hatchet Go SDK 装配进 Fx：client、可选 worker、
// 事件推送（outbox relay）、workflow 运行以及语义 actor 的辅助函数
// （[NewActor] / [CreateReminder]）。
//
// # 语义 actor（非 Dapr Actors）
//
// [NewActor] 提供并发邮箱：Hatchet GROUP_ROUND_ROBIN 配 MaxRuns=1，
// 以 CEL input.actorId 为键。同一 actorId 的运行串行；不同
// id 可以并行。它不是完整的 Dapr Actor / 粘性实体 worker：
//
//   - 运行之间没有进程亲和性（粘性只作用于单次运行内的 step）
//   - 没有内存内激活；状态请持久化在处理器之外（DB、appkv 等）
//   - Call/CallAsync 要求载荷 JSON 序列化出 "actorId"（嵌入
//     [ActorRef] 或带 json:"actorId" 的字段），并发 CEL 才能匹配
//
// # 定时提醒
//
// [CreateReminder] 是周期性 Hatchet cron，不是完整的 Dapr Actor Reminder：
// 只有 period（没有 dueTime / 一次性），错过的滴答不补跑，
// create 也不是 upsert（Name+Expression 的唯一性可能拒绝重复）。
// 一次性定时运行请使用 [ScheduleOnce]。
//
// # 事件即 MQ
//
// [Client.PushEvent] 触发注册了匹配 event key 的任务。
// 在任务注册之前到达的事件不会重放。生产者持久性优先用
// 事务性 outbox；消费者幂等性用 inbox.Once
// （至少一次投递）。
//
// 通过 hatchet.enabled=true 和 HATCHET_CLIENT_TOKEN（或 hatchet.token）启用。
// 当 hatchet.otel 为 true（默认）且 otelx 已安装 SDK TracerProvider 时，
// 会挂上 worker trace（hatchet.start_step_run）；见 attachWorkerTracing。
// 参见 https://docs.hatchet.run/
package hatchetx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	v0Client "github.com/hatchet-dev/hatchet/pkg/client"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/hatchet-dev/hatchet/sdks/go/features"
	"github.com/tidwall/gjson"
)

// Client 是 Hatchet SDK client 的包装。hatchet 禁用时为 nil。
type Client struct {
	SDK *hatchet.Client
}

// WorkflowBase 为 worker 注册而重新导出。
type WorkflowBase = hatchet.WorkflowBase

// Registrar 向共享 worker 贡献 workflow / task。
type Registrar func(client *Client) ([]WorkflowBase, error)

// Module 提供 [Client]，并在启用时为所有 [Registrar] 启动 worker。
//
// Registrar 的读取必须垂到 OnStart：group 是容器内的可变状态，装配期（Option
// 闭包内）读到的只是「当前已装配部分」，业务模块稍后注册的 workflow 会全部丢失。
var Module = di.Module("fxkit/hatchetx",
	config.Provide[Config]("hatchet"),
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*Client, error) {
			cfg, err := di.Invoke[*Config](i)
			if err != nil {
				return nil, err
			}
			return NewClient(cfg)
		})
		provideOutboxPublisherOption()(i)

		lc, _ := di.Invoke[*di.Lifecycle](i)
		registerLifecycle(lifecycleParams{
			LC:       lc,
			Injector: i,
		})
	},
)

// ProvideRegistrar 把 [Registrar] 发布到 hatchet_registrars group。
//
// 支持：Registrar / func(*Client) ([]WorkflowBase, error) /
// func(*Client) (WorkflowBase, error) / func() Registrar / func(di.Injector) Registrar，
// 以及任意 func(dep...) Registrar、func(dep...) (Registrar, error)。
// 后者延迟到 PreStart 解析依赖后调用；无法识别的签名直接 panic——
// 静默丢弃会导致「workflow 写了但 worker 从不执行」。
func ProvideRegistrar(fn any) di.Option {
	return func(i di.Injector) {
		if fn == nil {
			return
		}
		if reg, ok := asRegistrar(fn); ok {
			di.ProvideGroupItem[Registrar](i, reg)
			return
		}
		switch f := fn.(type) {
		case func() Registrar:
			di.ProvideGroupItem[Registrar](i, f())
			return
		case func(di.Injector) Registrar:
			di.ProvideGroupItem[Registrar](i, f(i))
			return
		}

		val := reflect.ValueOf(fn)
		if val.Kind() != reflect.Func {
			panic("hatchetx.ProvideRegistrar: unsupported argument " + reflect.TypeOf(fn).String() +
				"; want a Registrar, a WorkflowBase constructor, or a constructor returning Registrar")
		}
		if err := di.ValidateConstructor(val.Type()); err != nil {
			panic("hatchetx.ProvideRegistrar: " + err.Error())
		}
		lc, err := di.Invoke[*di.Lifecycle](i)
		if err != nil || lc == nil {
			panic("hatchetx.ProvideRegistrar: constructor form requires *di.Lifecycle in the graph")
		}
		lc.AppendPreStartPhase(di.PhaseResolveContributors, di.Hook{
			OnStart: func(context.Context) error {
				out, callErr := di.CallConstructor(i, fn)
				if callErr != nil {
					return fmt.Errorf("hatchetx.ProvideRegistrar: %w", callErr)
				}
				reg, ok := asRegistrar(out)
				if !ok {
					return fmt.Errorf("hatchetx.ProvideRegistrar: constructor returned %T, want hatchetx.Registrar", out)
				}
				di.ProvideGroupItem[Registrar](i, reg)
				return nil
			},
		})
	}
}

// asRegistrar 把受支持的 Registrar 形态归一为 [Registrar]。
func asRegistrar(v any) (Registrar, bool) {
	switch f := v.(type) {
	case Registrar:
		return f, true
	case func(*Client) ([]WorkflowBase, error):
		return Registrar(f), true
	case func(*Client) (WorkflowBase, error):
		return func(c *Client) ([]WorkflowBase, error) {
			w, err := f(c)
			if err != nil {
				return nil, err
			}
			if w == nil {
				return nil, nil
			}
			return []WorkflowBase{w}, nil
		}, true
	}
	return nil, false
}

// ProvideWorkflow 把单个原生 Hatchet workflow 直接注册到 worker。
func ProvideWorkflow(wf WorkflowBase) di.Option {
	return ProvideRegistrar(func(*Client) ([]WorkflowBase, error) {
		if wf == nil {
			return nil, nil
		}
		return []WorkflowBase{wf}, nil
	})
}

// ProvideWorkflows 把多个原生 Hatchet workflow 直接注册到 worker。
func ProvideWorkflows(wfs ...WorkflowBase) di.Option {
	return ProvideRegistrar(func(*Client) ([]WorkflowBase, error) {
		var res []WorkflowBase
		for _, w := range wfs {
			if w != nil {
				res = append(res, w)
			}
		}
		return res, nil
	})
}

// ProvideWorkflowFn 注册一个使用 Client 构建 workflow 的构造函数。
func ProvideWorkflowFn(fn func(*Client) ([]WorkflowBase, error)) di.Option {
	return ProvideRegistrar(fn)
}

// NewClient 在 hatchet.enabled 为 true 时构建 Hatchet client。
//
// V1 Go SDK 从 HATCHET_CLIENT_* 环境变量读取连接配置
// （见 https://docs.hatchet.run/home/migration-guide-go）。当对应环境变量未设置时，
// yaml / 配置值会作为环境变量默认值注入——从而避开
// 已废弃的 pkg/client WithToken / WithHostPort / WithNamespace 选项。
func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil || !cfg.Enabled {
		return &Client{}, nil
	}
	if err := applyHatchetEnv(*cfg); err != nil {
		return nil, err
	}
	sdk, err := hatchet.NewClient()
	if err != nil {
		return nil, fmt.Errorf("hatchetx: %w", err)
	}
	return &Client{SDK: sdk}, nil
}

var envMu sync.Mutex

// applyHatchetEnv 把 fxkit 的 yaml 配置桥接为 SDK 读取的环境变量。
// 已存在的进程环境变量优先（不覆盖）。
func applyHatchetEnv(hcfg Config) error {
	envMu.Lock()
	defer envMu.Unlock()
	if hcfg.Token != "" {
		setenvIfEmpty("HATCHET_CLIENT_TOKEN", hcfg.Token)
	}
	if hp := strings.TrimSpace(hcfg.HostPort); hp != "" {
		if _, _, err := net.SplitHostPort(hp); err != nil {
			return fmt.Errorf("hatchetx: host_port: want host:port, got %q", hcfg.HostPort)
		}
		setenvIfEmpty("HATCHET_CLIENT_HOST_PORT", hp)
	}
	if u := strings.TrimSpace(hcfg.ServerURL); u != "" {
		setenvIfEmpty("HATCHET_CLIENT_SERVER_URL", u)
	}
	if hcfg.Namespace != "" {
		setenvIfEmpty("HATCHET_CLIENT_NAMESPACE", hcfg.Namespace)
	}

	effectiveHostPort := strings.TrimSpace(os.Getenv("HATCHET_CLIENT_HOST_PORT"))
	if effectiveHostPort == "" {
		effectiveHostPort = strings.TrimSpace(hcfg.HostPort)
	}

	// 本地 compose（SERVER_GRPC_INSECURE=t）需要明文 gRPC。
	// 环境变量未设置时以 yaml tls_strategy 为准；否则只有 loopback 默认 none。
	if s := strings.TrimSpace(hcfg.TLSStrategy); s != "" {
		setenvIfEmpty("HATCHET_CLIENT_TLS_STRATEGY", s)
	} else if isLoopbackHostPort(effectiveHostPort) {
		setenvIfEmpty("HATCHET_CLIENT_TLS_STRATEGY", "none")
	}
	return nil
}

func isLoopbackHostPort(hp string) bool {
	hp = strings.TrimSpace(hp)
	if hp == "" {
		return false
	}
	host, _, err := net.SplitHostPort(hp)
	if err != nil {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

func setenvIfEmpty(key, value string) {
	if os.Getenv(key) == "" {
		_ = os.Setenv(key, value)
	}
}

// Enabled 报告 SDK client 是否可用。
func (c *Client) Enabled() bool {
	return c != nil && c.SDK != nil
}

// Raw 返回底层 Hatchet SDK client 作为逃生通道，禁用时为 nil。
func (c *Client) Raw() *hatchet.Client {
	if !c.Enabled() {
		return nil
	}
	return c.SDK
}

// PushEvent 发布 Hatchet 事件（触发基于事件的任务 / workflow）。
func (c *Client) PushEvent(ctx context.Context, eventKey string, payload any, metadata map[string]string) error {
	opts := []v0Client.PushOpFunc{}
	if len(metadata) > 0 {
		opts = append(opts, v0Client.WithEventMetadata(metadata))
	}
	return c.PushEventWithOpts(ctx, eventKey, payload, opts...)
}

// PushEventWithOpts 使用完整的推送选项发布 Hatchet 事件。
func (c *Client) PushEventWithOpts(ctx context.Context, eventKey string, payload any, opts ...v0Client.PushOpFunc) error {
	if !c.Enabled() {
		return fmt.Errorf("hatchetx: client disabled")
	}
	return c.SDK.Events().Push(ctx, eventKey, payload, opts...)
}

// PushEventJSON 把原始 JSON 字节作为事件推送，避免多余的 map 反序列化。
func (c *Client) PushEventJSON(ctx context.Context, eventKey string, raw []byte, metadata map[string]string) error {
	var payload any
	if len(raw) == 0 {
		payload = json.RawMessage("{}")
	} else if gjson.ValidBytes(raw) {
		payload = json.RawMessage(raw)
	} else {
		payload = map[string]any{"raw": string(raw)}
	}
	return c.PushEvent(ctx, eventKey, payload, metadata)
}

// Run 同步启动 workflow，阻塞直到执行完成并返回结果。
func (c *Client) Run(ctx context.Context, workflowName string, input any, opts ...hatchet.RunOptFunc) (*hatchet.WorkflowResult, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	return c.SDK.Run(ctx, workflowName, input, opts...)
}

// RunNoWait 异步启动 workflow 并返回 run id。支持可选的 Hatchet RunOptFunc 选项。
func (c *Client) RunNoWait(ctx context.Context, workflowName string, input any, opts ...hatchet.RunOptFunc) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("hatchetx: client disabled")
	}
	ref, err := c.SDK.RunNoWait(ctx, workflowName, input, opts...)
	if err != nil {
		return "", err
	}
	return ref.RunId, nil
}

// RunMany 批量触发多个 workflow 运行。
func (c *Client) RunMany(ctx context.Context, workflowName string, inputs []hatchet.RunManyOpt) ([]hatchet.WorkflowRunRef, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	return c.SDK.RunMany(ctx, workflowName, inputs)
}

// NewWorkflow 创建新的 Hatchet workflow 定义。
func (c *Client) NewWorkflow(name string, opts ...hatchet.WorkflowOption) *hatchet.Workflow {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.NewWorkflow(name, opts...)
}

// NewStandaloneTask 创建 standalone task workflow。
func (c *Client) NewStandaloneTask(name string, fn any, opts ...hatchet.StandaloneTaskOption) *hatchet.StandaloneTask {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.NewStandaloneTask(name, fn, opts...)
}

// NewStandaloneDurableTask 创建带状态持久化的 standalone durable task workflow。
func (c *Client) NewStandaloneDurableTask(name string, fn any, opts ...hatchet.StandaloneTaskOption) *hatchet.StandaloneTask {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.NewStandaloneDurableTask(name, fn, opts...)
}

// Crons 返回 Hatchet cron 触发器 client，禁用时为 nil。
func (c *Client) Crons() *features.CronsClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Crons()
}

// Runs 返回 Hatchet workflow 运行 client，禁用时为 nil。
func (c *Client) Runs() *features.RunsClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Runs()
}

// Schedules 返回 Hatchet 定时运行 client，禁用时为 nil。
func (c *Client) Schedules() *features.SchedulesClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Schedules()
}

// RateLimits 返回 Hatchet 限流 client，禁用时为 nil。
func (c *Client) RateLimits() *features.RateLimitsClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.RateLimits()
}

// Events 返回 Hatchet 事件 client，禁用时为 nil。
func (c *Client) Events() v0Client.EventClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Events()
}

// Workflows 返回 Hatchet workflow client，禁用时为 nil。
func (c *Client) Workflows() *features.WorkflowsClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Workflows()
}

// Workers 返回 Hatchet worker client，禁用时为 nil。
func (c *Client) Workers() *features.WorkersClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Workers()
}

// Filters 返回 Hatchet filter client，禁用时为 nil。
func (c *Client) Filters() *features.FiltersClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Filters()
}

// Metrics 返回 Hatchet 指标 client，禁用时为 nil。
func (c *Client) Metrics() *features.MetricsClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Metrics()
}

// Logs 返回 Hatchet 日志 client，禁用时为 nil。
func (c *Client) Logs() *features.LogsClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Logs()
}

// CEL 返回 Hatchet CEL 表达式 client，禁用时为 nil。
func (c *Client) CEL() *features.CELClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.CEL()
}

// Webhooks 返回 Hatchet webhook client，禁用时为 nil。
func (c *Client) Webhooks() *features.WebhooksClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Webhooks()
}

type lifecycleParams struct {
	LC       *di.Lifecycle
	Injector di.Injector
}

func registerLifecycle(p lifecycleParams) {
	if p.LC == nil || p.Injector == nil {
		return
	}

	var (
		cleanup func() error
		started bool
	)
	workerName := "fxkit-worker"

	p.LC.Append(di.Hook{
		OnStart: func(ctx context.Context) error {
			cfg, err := di.Invoke[*Config](p.Injector)
			if err != nil {
				return fmt.Errorf("hatchetx: resolve config: %w", err)
			}
			if cfg == nil || !cfg.Enabled {
				slog.Info("hatchetx: disabled")
				return nil
			}

			client, err := di.Invoke[*Client](p.Injector)
			if err != nil {
				return fmt.Errorf("hatchetx: resolve client: %w", err)
			}
			if client == nil || !client.Enabled() {
				return fmt.Errorf("hatchetx: hatchet.enabled=true but client is not initialized (check token/host_port)")
			}

			if n := strings.TrimSpace(cfg.WorkerName); n != "" {
				workerName = n
			}

			// group 在 OnStart 读取：此时所有模块（含宿主业务模块）都已装配完毕。
			registrars := di.GetGroup[Registrar](p.Injector)
			var workflows []WorkflowBase
			for _, reg := range registrars {
				if reg == nil {
					continue
				}
				ws, err := reg(client)
				if err != nil {
					return err
				}
				workflows = append(workflows, ws...)
			}
			if len(workflows) == 0 {
				slog.Info("hatchetx: enabled but no workflows registered; worker not started",
					"registrars", len(registrars))
				return nil
			}

			workerOpts := []hatchet.WorkerOption{
				hatchet.WithWorkflows(workflows...),
			}
			if cfg.WorkerSlots > 0 {
				workerOpts = append(workerOpts, hatchet.WithSlots(cfg.WorkerSlots))
			}
			if len(cfg.WorkerLabels) > 0 {
				labels := make(map[string]any, len(cfg.WorkerLabels))
				for k, v := range cfg.WorkerLabels {
					labels[k] = v
				}
				workerOpts = append(workerOpts, hatchet.WithLabels(labels))
			}

			w, err := client.SDK.NewWorker(workerName, workerOpts...)
			if err != nil {
				return fmt.Errorf("hatchetx: new worker: %w", err)
			}
			attachWorkerTracing(w, cfg.OTel)
			cleanup, err = w.Start()
			if err != nil {
				return fmt.Errorf("hatchetx: start worker: %w", err)
			}
			started = true
			slog.Info("hatchetx: worker started", "name", workerName, "workflows", len(workflows))
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if !started || cleanup == nil {
				return nil
			}
			if err := cleanup(); err != nil {
				slog.Warn("hatchetx: worker cleanup", "error", err)
				return err
			}
			slog.Info("hatchetx: worker stopped")
			return nil
		},
	})
}
