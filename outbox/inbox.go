package outbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fitan/fxkit/gormx"
	"gorm.io/gorm/clause"
)

// InboxEvent records a successfully processed consumer idempotency key.
// Use with [Inbox.Once] so at-least-once deliveries from the outbox relay are
// applied at most once on the consumer side.
type InboxEvent struct {
	ID          string    `gorm:"primaryKey;size:128"`
	Topic       string    `gorm:"size:255;not null"`
	ProcessedAt time.Time `gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (InboxEvent) TableName() string { return "outbox_inbox_events" }

// Inbox provides consumer-side exactly-once processing helpers (at-most-once
// application of handlers) keyed by producer [Message.IdempotencyKey].
type Inbox struct {
	client *gormx.Client
}

// NewInbox returns an Inbox backed by client.
func NewInbox(client *gormx.Client) *Inbox {
	return &Inbox{client: client}
}

// Migrate creates the inbox table. Called from [Module] when outbox is enabled.
func (in *Inbox) Migrate(ctx context.Context) error {
	if in == nil || in.client == nil {
		return fmt.Errorf("outbox: nil inbox")
	}
	return in.client.Conn(ctx).AutoMigrate(&InboxEvent{})
}

// OnceInput is the parameter struct for [Inbox.Once].
type OnceInput struct {
	Key   string
	Topic string
	Fn    func(context.Context) error
}

// Once runs fn at most once for key. Empty key always runs fn (no dedup).
// If key was already processed, Once returns nil without calling fn.
// fn and the inbox insert share one transaction so a failed fn does not mark the key.
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

	// Insert-then-Fn in one transaction: the unique PK is the claim.
	// ON CONFLICT DO NOTHING so a duplicate key does not abort PostgreSQL.
	// If Fn fails, the insert rolls back so the key can be retried.
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

// OnceResultInput is the parameter struct for [Inbox.OnceResult].
type OnceResultInput[R any] struct {
	Key   string
	Topic string
	Fn    func(context.Context) (R, error)
}

// OnceOutcome records the execution result and whether it was processed or deduplicated.
type OnceOutcome[R any] struct {
	Result    R
	Processed bool // true if fn was executed; false if skipped due to idempotency key duplication
}

// OnceResult runs fn at most once for key and returns the generic computation result.
// Leverages Go 1.27+ generic methods on types to eliminate external variable allocations.
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
