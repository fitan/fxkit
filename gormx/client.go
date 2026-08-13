package gormx

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

type txKey struct{}

// WithTx 返回携带活跃 GORM 事务句柄的子 context。与 [Client.Conn] 配合，使嵌套逻辑在同一 tx 上执行。
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFromContext 返回 [WithTx] 存入的事务（若有）。
func TxFromContext(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	return tx, ok && tx != nil
}

// Client 包装根连接池。应用代码应使用 [Client.Conn] 做请求级访问（trace/cancel），
// 使用 [Client.Transaction] 做事务范围。仅在需要长期 *gorm.DB 句柄的集成时使用 [Client.Pool]。
type Client struct {
	pool *gorm.DB
}

// NewTestClient wraps an existing DB handle for unit tests.
func NewTestClient(db *gorm.DB) *Client {
	return &Client{pool: db}
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
	return c.pool.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(WithTx(ctx, tx))
	})
}
