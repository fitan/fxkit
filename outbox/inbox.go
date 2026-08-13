package outbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fitan/fxkit/gormx"
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
	// Concurrent losers hit unique violation and skip Fn (at-most-once).
	// If Fn fails, the insert rolls back so the key can be retried.
	return in.client.Transaction(ctx, func(txCtx context.Context) error {
		row := InboxEvent{
			ID:          key,
			Topic:       inp.Topic,
			ProcessedAt: time.Now().UTC(),
		}
		if err := in.client.Conn(txCtx).Create(&row).Error; err != nil {
			if isUniqueViolation(err) {
				return nil
			}
			return err
		}
		return inp.Fn(txCtx)
	})
}
