package outbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fitan/fxkit/gormx"
	"gorm.io/gorm/clause"
)

// InboxEvent 记录一条已成功处理的消费端幂等 key。
// 与 [Inbox.Once] 配合使用，使 outbox relay 的至少一次投递
// 在消费端最多被应用一次。
type InboxEvent struct {
	ID          string    `gorm:"primaryKey;size:128"`
	Topic       string    `gorm:"size:255;not null"`
	ProcessedAt time.Time `gorm:"not null"`
}

// TableName 覆盖 GORM 默认表名。
func (InboxEvent) TableName() string { return "outbox_inbox_events" }

// Inbox 提供消费端的精确一次处理辅助（处理器最多应用一次），
// 以生产端的 [Message.IdempotencyKey] 为键。
type Inbox struct {
	client *gormx.Client
}

// NewInbox 返回由 client 支撑的 Inbox。
func NewInbox(client *gormx.Client) *Inbox {
	return &Inbox{client: client}
}

// Migrate 创建 inbox 表。outbox 启用时由 [Module] 调用。
func (in *Inbox) Migrate(ctx context.Context) error {
	if in == nil || in.client == nil {
		return fmt.Errorf("outbox: nil inbox")
	}
	return in.client.Conn(ctx).AutoMigrate(&InboxEvent{})
}

// OnceInput 是 [Inbox.Once] 的参数结构体。
type OnceInput struct {
	Key   string
	Topic string
	Fn    func(context.Context) error
}

// Once 对 key 最多执行一次 fn。key 为空时总是执行 fn（不做去重）。
// 若 key 已处理过，Once 直接返回 nil 而不调用 fn。
// fn 与 inbox 插入共用同一个事务，因此 fn 失败不会把该 key 标记为已处理。
func (in *Inbox) Once(ctx context.Context, inp OnceInput) error {
	if in == nil || in.client == nil {
		return fmt.Errorf("outbox: nil inbox")
	}
	key := strings.TrimSpace(inp.Key)
	if key == "" {
		if inp.Fn == nil {
			return nil
		}
		return inp.Fn(ctx)
	}
	if inp.Fn == nil {
		return fmt.Errorf("outbox: inbox Once requires Fn")
	}

	// 在同一个事务内先插入再执行 Fn：唯一主键即是认领。
	// 使用 ON CONFLICT DO NOTHING，避免重复 key 中止 PostgreSQL 事务。
	// 若 Fn 失败，插入会回滚，因此该 key 仍可重试。
	return in.client.Transaction(ctx, func(txCtx context.Context) error {
		row := InboxEvent{
			ID:          key,
			Topic:       inp.Topic,
			ProcessedAt: time.Now().UTC(),
		}
		res := in.client.Conn(txCtx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		return inp.Fn(txCtx)
	})
}

// OnceResultInput 是 [Inbox.OnceResult] 的参数结构体。
type OnceResultInput[R any] struct {
	Key   string
	Topic string
	Fn    func(context.Context) (R, error)
}

// OnceOutcome 记录执行结果，以及本次是执行了还是被去重。
type OnceOutcome[R any] struct {
	Result    R
	Processed bool // true 表示 fn 已执行；false 表示因幂等 key 重复而跳过
}

// OnceResult 对 key 最多执行一次 fn，并返回泛型计算结果。
// 利用 Go 1.27+ 的泛型方法能力，省去外部临时变量分配。
func (in *Inbox) OnceResult[R any](ctx context.Context, inp OnceResultInput[R]) (OnceOutcome[R], error) {
	var zero OnceOutcome[R]
	if in == nil || in.client == nil {
		return zero, fmt.Errorf("outbox: nil inbox")
	}
	key := strings.TrimSpace(inp.Key)
	if key == "" {
		if inp.Fn == nil {
			return zero, nil
		}
		res, err := inp.Fn(ctx)
		if err != nil {
			return zero, err
		}
		return OnceOutcome[R]{Result: res, Processed: true}, nil
	}
	if inp.Fn == nil {
		return zero, fmt.Errorf("outbox: inbox OnceResult requires Fn")
	}

	var outcome OnceOutcome[R]
	err := in.client.Transaction(ctx, func(txCtx context.Context) error {
		row := InboxEvent{
			ID:          key,
			Topic:       inp.Topic,
			ProcessedAt: time.Now().UTC(),
		}
		res := in.client.Conn(txCtx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			outcome = OnceOutcome[R]{Processed: false}
			return nil
		}
		val, err := inp.Fn(txCtx)
		if err != nil {
			return err
		}
		outcome = OnceOutcome[R]{Result: val, Processed: true}
		return nil
	})
	if err != nil {
		return zero, err
	}
	return outcome, nil
}
