package di

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// PreStart 阶段常用的优先级。数值小的先执行。
const (
	// PhaseResolveContributors 解析「延迟构造函数」并写入各自的 group。
	// 形如 func(*Service) Registrar / func(*Service) []Route 的贡献点在这里
	// 才真正被求值，所以必须早于任何读取 group 的安装步骤。
	PhaseResolveContributors = 0
	// PhaseWireGroup 在装配完成后「固化」容器内容（把 group 装进 huma 等）。
	PhaseWireGroup = 100
	// PhaseAfterRegistrars 在 huma registrar 安装完成之后、spec 消费者之前运行。
	// 典型用途：把 HTTPRoute 登记成 Casbin 策略——它需要完整的 operation 集合。
	//
	// 早期调用方写 `PhaseWireGroup+1`；偏移量是隐式契约，改一个值就会错位。
	PhaseAfterRegistrars = 110
	// PhasePostWire 在 PhaseWireGroup 之后运行，用于依赖「已安装完成」结果的步骤，
	// 例如从 live Huma spec 生成 /docs 路由、从 Huma 提取 MCP 工具。
	PhasePostWire = 150
	// PhaseMountHTTP 把固化后的路由挂到 http mux。晚于 PhasePostWire。
	PhaseMountHTTP = 200
	// PhaseAfterHTTPServer 在路由已挂载、*HTTPServer 已构造之后运行。
	// 典型用途：服务自注册（必须先绑定端口）。
	//
	// 早期调用方写 `PhaseMountHTTP+10` / `PhaseMountHTTP+1`；偏移量不可读且易错。
	PhaseAfterHTTPServer = 210
)

// Hook 定义服务的启动和停止钩子。
type Hook struct {
	// OnStart 启动钩子；PreStart 与 OnStart 两个阶段都会执行它，返回错误会触发逆序回滚。
	OnStart func(ctx context.Context) error
	// OnStop 关闭钩子；[Lifecycle.Stop] 时逆序执行，nil 表示无需清理。
	OnStop func(ctx context.Context) error
}

// phasedHook 是一条带优先级与注册序号的 PreStart 钩子。
type phasedHook struct {
	// priority 是阶段优先级，数值小的先执行（见 [PhaseResolveContributors] 等常量）。
	priority int
	// seq 是注册序号，同优先级时按它稳定排序。
	seq int
	// hook 是实际的钩子函数。
	hook Hook
}

// Lifecycle 管理应用的生命周期钩子。
//
// 钩子分两个阶段执行，顺序固定：
//
//  1. PreStart：所有对象图装配完成后、任何服务对外可用之前运行。
//     用于「固化」容器（把 fx group 里的路由/中间件/Registrar 一次性读出并安装），
//     以及把固化结果挂到 http mux。因为只有装配全部结束后读取 group 才是完整的，
//     这一步必须晚于装配、早于 OnStart。PreStart 内部按优先级排序
//     （见 [PhaseWireGroup] / [PhaseMountHTTP]），同优先级按注册顺序。
//  2. OnStart：按注册顺序启动服务（监听端口、启动 worker 等）。
//
// 关闭时 OnStop 逆序执行。任一 PreStart/OnStart 失败都会逆序回滚已启动的钩子。
type Lifecycle struct {
	// mu 保护 preStart/hooks/seq 的并发访问。
	mu sync.Mutex
	// preStart 是按优先级排序执行的 PreStart 钩子集合。
	preStart []phasedHook
	// hooks 是按注册顺序保存的 OnStart/OnStop 钩子。
	hooks []Hook
	// seq 是 PreStart 钩子的注册序号计数器。
	seq int
}

// NewLifecycle 创建一个新的 Lifecycle 实例。
func NewLifecycle() *Lifecycle {
	return &Lifecycle{
		hooks: make([]Hook, 0),
	}
}

// Append 添加生命周期钩子。
func (lc *Lifecycle) Append(hook Hook) {
	if lc == nil {
		return
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.hooks = append(lc.hooks, hook)
}

// AppendPreStart 添加在装配完成后、OnStart 之前运行的钩子（[PhaseWireGroup] 优先级）。
func (lc *Lifecycle) AppendPreStart(hook Hook) {
	lc.AppendPreStartPhase(PhaseWireGroup, hook)
}

// AppendPreStartPhase 以显式优先级添加 PreStart 钩子。数值小的先执行，
// 同优先级按注册顺序稳定执行。
func (lc *Lifecycle) AppendPreStartPhase(priority int, hook Hook) {
	if lc == nil {
		return
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.seq++
	lc.preStart = append(lc.preStart, phasedHook{
		priority: priority,
		seq:      lc.seq,
		hook:     hook,
	})
}

// orderedPreStart 返回按 (priority, 注册顺序) 排好的 PreStart 钩子。
func (lc *Lifecycle) orderedPreStart() []phasedHook {
	lc.mu.Lock()
	phased := append([]phasedHook(nil), lc.preStart...)
	lc.mu.Unlock()

	sort.SliceStable(phased, func(a, b int) bool {
		if phased[a].priority != phased[b].priority {
			return phased[a].priority < phased[b].priority
		}
		return phased[a].seq < phased[b].seq
	})
	return phased
}

// StartPreStartOnly 只运行 PreStart 阶段，不启动任何服务。
//
// 用于「只构建对象图」的场景（`serve --di-graph`、CI 自检）：
// 装配完成 → 解析延迟构造函数 → 固化 group → 输出诊断，然后直接退出。
// 不会监听端口、不会启动 worker。
func (lc *Lifecycle) StartPreStartOnly(ctx context.Context) error {
	if lc == nil {
		return nil
	}
	for _, p := range lc.orderedPreStart() {
		h := p.hook
		if h.OnStart == nil {
			continue
		}
		if err := h.OnStart(ctx); err != nil {
			return fmt.Errorf("lifecycle pre-start hook failed: %w", err)
		}
	}
	return nil
}

// Start 先按优先级运行 PreStart，再按注册顺序运行 OnStart。
// 任一钩子失败会立即按逆序执行已启动钩子的 OnStop 回滚。
func (lc *Lifecycle) Start(ctx context.Context) error {
	if lc == nil {
		return nil
	}

	var started []Hook
	rollback := func() {
		for j := len(started) - 1; j >= 0; j-- {
			if started[j].OnStop != nil {
				_ = started[j].OnStop(ctx)
			}
		}
	}

	for i, p := range lc.orderedPreStart() {
		h := p.hook
		if h.OnStart == nil {
			continue
		}
		start := time.Now()
		err := h.OnStart(ctx)
		recordPreStartPhase(ctx, p.priority, time.Since(start), err)
		if err != nil {
			slog.Error("lifecycle pre-start failed; rolling back", "error", err, "step", i)
			rollback()
			return fmt.Errorf("lifecycle pre-start hook failed: %w", err)
		}
		started = append(started, h)
	}

	// PreStart 可能追加新的 OnStart 钩子（例如 listener 在挂载路由时创建
	// HTTPServer 并注册自己的 OnStart），所以此处重新取快照。
	lc.mu.Lock()
	main := append([]Hook(nil), lc.hooks...)
	lc.mu.Unlock()

	for i, h := range main {
		if h.OnStart == nil {
			continue
		}
		if err := h.OnStart(ctx); err != nil {
			slog.Error("lifecycle start failed; rolling back started hooks", "error", err, "step", i)
			rollback()
			return fmt.Errorf("lifecycle hook start failed: %w", err)
		}
		started = append(started, h)
	}

	// OnStart 里 Append 的钩子不会被本次 Start 执行（快照已取）。这是历史上
	// listener 不监听的根因，这里显式告警，避免再次静默发生。
	lc.mu.Lock()
	finalLen := len(lc.hooks)
	lc.mu.Unlock()
	if finalLen != len(main) {
		slog.Warn("lifecycle: hooks appended during OnStart were not started; append them from a PreStart hook instead",
			"appended", finalLen-len(main))
	}
	return nil
}

// Stop 按逆序运行所有 OnStop 钩子，即使某个失败也会尝试继续关闭后续服务。
func (lc *Lifecycle) Stop(ctx context.Context) error {
	if lc == nil {
		return nil
	}

	var firstErr error
	errs := func(err error) {
		slog.Error("lifecycle hook stop failed", "error", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	lc.mu.Lock()
	hooks := append([]Hook(nil), lc.hooks...)
	lc.mu.Unlock()
	for i := len(hooks) - 1; i >= 0; i-- {
		if hooks[i].OnStop != nil {
			if err := hooks[i].OnStop(ctx); err != nil {
				errs(err)
			}
		}
	}

	pre := lc.orderedPreStart()
	for i := len(pre) - 1; i >= 0; i-- {
		if pre[i].hook.OnStop != nil {
			if err := pre[i].hook.OnStop(ctx); err != nil {
				errs(err)
			}
		}
	}
	return firstErr
}

// RequireStart 启动生命周期，失败时 panic（用于测试）。
func (lc *Lifecycle) RequireStart() {
	if err := lc.Start(context.Background()); err != nil {
		panic(fmt.Sprintf("lifecycle start failed: %v", err))
	}
}

// RequireStop 停止生命周期，失败时 panic（用于测试）。
func (lc *Lifecycle) RequireStop() {
	if err := lc.Stop(context.Background()); err != nil {
		panic(fmt.Sprintf("lifecycle stop failed: %v", err))
	}
}
