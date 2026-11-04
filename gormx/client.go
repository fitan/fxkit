package gormx

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"gorm.io/gorm"
)

type txKey struct{}

type afterCommitKey struct{}

type afterCommitHooks struct {
	mu  sync.Mutex
	fns []func(context.Context)
	// ctx 是不带事务句柄的提交后 context（见 Transaction）。
	ctx context.Context
}

func (h *afterCommitHooks) add(fn func(context.Context)) {
	if h == nil || fn == nil {
		return
	}
	h.mu.Lock()
	h.fns = append(h.fns, fn)
	h.mu.Unlock()
}

func (h *afterCommitHooks) run() {
	if h == nil {
		return
	}
	h.mu.Lock()
	fns := append([]func(context.Context){}, h.fns...)
	ctx := h.ctx
	h.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	for _, fn := range fns {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("gormx AfterCommit panic", "error", r)
				}
			}()
			fn(ctx)
		}()
	}
}

// WithTx 返回携带活跃 GORM 事务句柄的子 context。与 [Client.Conn] 配合，使嵌套逻辑在同一 tx 上执行。
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFromContext 返回 [WithTx] 存入的事务（若有）。
func TxFromContext(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	return tx, ok && tx != nil
}

// AfterCommit 在 ctx 所属事务成功提交后运行 fn。无外层 [Client.Transaction] 时立即执行。
// 嵌套 Transaction 共用外层 hook 列表，全部在最外层 commit 之后按注册顺序运行。
//
// fn 收到的是**不带事务句柄**的 context：在回调里用 [Client.Conn] 会走连接池，
// 而不是已经提交/回滚完毕的事务。早期实现让回调闭包自己捕获事务 ctx，导致
// 回调内的查询必然报 "transaction has already been committed or rolled back"。
//
// 注意：[WithTx] 只携带事务、没有提交边界。用它包裹的事务里调用 AfterCommit
// 无法推迟执行，本函数会告警并立即执行——需要「提交后」语义请用 [Client.Transaction]。
func AfterCommit(ctx context.Context, fn func(context.Context)) {
	if fn == nil {
		return
	}
	if ctx != nil {
		if h, ok := ctx.Value(afterCommitKey{}).(*afterCommitHooks); ok && h != nil {
			h.add(fn)
			return
		}
		if _, ok := TxFromContext(ctx); ok {
			slog.Warn("gormx: AfterCommit called with a WithTx context (no commit boundary); running immediately")
		}
	} else {
		ctx = context.Background()
	}
	fn(ctx)
}

// Client 包装根连接池。应用代码应使用 [Client.Conn] 做请求级访问（trace/cancel），
// 使用 [Client.Transaction] 做事务范围。仅在需要长期 *gorm.DB 句柄的集成时使用 [Client.Pool]。
type Client struct {
	pool        *gorm.DB
	autoMigrate bool
}

// NewTestClient 包装一个已有的 DB 句柄用于单元测试。
// 测试依赖 AutoMigrate，因此这里开启（生产默认关闭）。
func NewTestClient(db *gorm.DB) *Client {
	return &Client{pool: db, autoMigrate: true}
}

// AutoMigrate 报告是否允许框架自动建表（db.auto_migrate）。
// 生产应由 SQL 上线平台执行 gormx/migrations 下的 DDL。
func (c *Client) AutoMigrate() bool {
	return c != nil && c.autoMigrate
}

// Conn 返回绑定 ctx 的 *gorm.DB：若 ctx 上有活跃事务则用之，否则为根池的 [gorm.DB.WithContext]。ctx 不可为 nil。
func (c *Client) Conn(ctx context.Context) *gorm.DB {
	if c == nil || c.pool == nil {
		return nil
	}
	if tx, ok := TxFromContext(ctx); ok {
		return tx.WithContext(ctx)
	}
	return c.pool.WithContext(ctx)
}

// Pool 返回根 *gorm.DB。供 adapter 与迁移使用 —— 非按请求查询（请用 [Client.Conn]）。
func (c *Client) Pool() *gorm.DB {
	if c == nil {
		return nil
	}
	return c.pool
}

// Transaction 在数据库事务中运行 fn。若 ctx 已由 [WithTx] 携带 tx，则直接调用 fn（嵌套 commit/rollback 由外层事务负责）。
func (c *Client) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if c == nil || c.pool == nil {
		return fmt.Errorf("gormx: nil client")
	}
	if _, ok := TxFromContext(ctx); ok {
		return fn(ctx)
	}
	hooks := &afterCommitHooks{ctx: ctx}
	ctx = context.WithValue(ctx, afterCommitKey{}, hooks)
	if err := c.pool.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(WithTx(ctx, tx))
	}); err != nil {
		return err
	}
	hooks.run()
	return nil
}

// WithTxResult 在事务中运行 fn 并直接返回结果 R。
// 利用 Go 1.27+ 泛型方法能力，避免外部在闭包外声明临时变量。
func (c *Client) WithTxResult[R any](ctx context.Context, fn func(txCtx context.Context) (R, error)) (R, error) {
	var result R
	err := c.Transaction(ctx, func(txCtx context.Context) error {
		res, err := fn(txCtx)
		if err != nil {
			return err
		}
		result = res
		return nil
	})
	return result, err
}
