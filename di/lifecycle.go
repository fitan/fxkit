package di

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Hook 定义服务的启动和停止钩子。
type Hook struct {
	OnStart func(ctx context.Context) error
	OnStop  func(ctx context.Context) error
}

// Lifecycle 管理应用的生命周期钩子，保证按依赖正序 OnStart、逆序 OnStop。
type Lifecycle struct {
	mu    sync.Mutex
	hooks []Hook
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

// Start 按注册顺序运行所有 OnStart 钩子。若某个钩子失败，已启动的钩子会立即按逆序执行 OnStop 回滚。
func (lc *Lifecycle) Start(ctx context.Context) error {
	if lc == nil {
		return nil
	}
	lc.mu.Lock()
	hooks := append([]Hook(nil), lc.hooks...)
	lc.mu.Unlock()

	for i, h := range hooks {
		if h.OnStart != nil {
			if err := h.OnStart(ctx); err != nil {
				slog.Error("lifecycle start failed; rolling back started hooks", "error", err, "step", i)
				// 回滚已启动的钩子
				for j := i - 1; j >= 0; j-- {
					if hooks[j].OnStop != nil {
						_ = hooks[j].OnStop(ctx)
					}
				}
				return fmt.Errorf("lifecycle hook start failed: %w", err)
			}
		}
	}
	return nil
}

// Stop 按逆序运行所有 OnStop 钩子，即使某个失败也会尝试继续关闭后续服务。
func (lc *Lifecycle) Stop(ctx context.Context) error {
	if lc == nil {
		return nil
	}
	lc.mu.Lock()
	hooks := append([]Hook(nil), lc.hooks...)
	lc.mu.Unlock()

	var firstErr error
	for i := len(hooks) - 1; i >= 0; i-- {
		if hooks[i].OnStop != nil {
			if err := hooks[i].OnStop(ctx); err != nil {
				slog.Error("lifecycle hook stop failed", "error", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	return firstErr
}

// RequireStart starts the lifecycle or panics/fails (for testing).
func (lc *Lifecycle) RequireStart() {
	if err := lc.Start(context.Background()); err != nil {
		panic(fmt.Sprintf("lifecycle start failed: %v", err))
	}
}

// RequireStop stops the lifecycle or panics/fails (for testing).
func (lc *Lifecycle) RequireStop() {
	if err := lc.Stop(context.Background()); err != nil {
		panic(fmt.Sprintf("lifecycle stop failed: %v", err))
	}
}
