package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fitan/fxkit/gormx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Message is a single outbox entry to publish after commit.
type Message struct {
	Pubsub  string
	Topic   string
	Payload []byte
	// IdempotencyKey optionally deduplicates producer-side Enqueue and is
	// forwarded as publish metadata ([MetadataIdempotencyKey]).
	IdempotencyKey string
}

// Store writes outbox rows using [gormx.Client.Conn], joining an outer transaction when present.
type Store struct {
	client *gormx.Client
}

// NewStore returns a Store backed by client. client must be non-nil for Enqueue.
func NewStore(client *gormx.Client) *Store {
	return &Store{client: client}
}

// Enqueue inserts a pending outbox row on the connection for ctx (same tx when ctx carries gormx.WithTx).
// When IdempotencyKey is set and a row with that key already exists, Enqueue returns nil (idempotent).
// If the existing row is failed, it is reset to pending so the producer can retry after max_retries.
func (s *Store) Enqueue(ctx context.Context, msg Message) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("outbox: nil store")
	}
	if msg.Topic == "" {
		return fmt.Errorf("outbox: topic is required")
	}
	if msg.Pubsub == "" {
		msg.Pubsub = "hatchet"
	}
	if len(msg.Payload) == 0 {
		return fmt.Errorf("outbox: empty payload")
	}
	row := OutboxEvent{
		Pubsub:  msg.Pubsub,
		Topic:   msg.Topic,
		Payload: msg.Payload,
		Status:  StatusPending,
	}
	if key := strings.TrimSpace(msg.IdempotencyKey); key != "" {
		row.IdempotencyKey = &key
		return enqueueIdempotent(s.client.Conn(ctx), row)
	}
	return s.client.Conn(ctx).Create(&row).Error
}

// enqueueIdempotent inserts a keyed row, or resets it only when the existing
// row is failed. Uses ON CONFLICT DO NOTHING so a unique violation does not
// abort a PostgreSQL transaction (Create-then-Updates would).
func enqueueIdempotent(db *gorm.DB, row OutboxEvent) error {
	res := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "idempotency_key"}},
		DoNothing: true,
	}).Create(&row)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	key := ""
	if row.IdempotencyKey != nil {
		key = *row.IdempotencyKey
	}
	return db.Model(&OutboxEvent{}).
		Where("idempotency_key = ? AND status = ?", key, StatusFailed).
		Updates(map[string]any{
			"status":     StatusPending,
			"attempts":   0,
			"last_error": "",
			"locked_at":  nil,
			"lease_id":   "",
			"payload":    row.Payload,
			"topic":      row.Topic,
			"pubsub":     row.Pubsub,
		}).Error
}

// EnqueueTopicInput is the struct form of [EnqueueTopicMsg].
// Topic is the Hatchet event key. PubsubName is retained for DB compatibility (defaults to "hatchet").
type EnqueueTopicInput[T any] struct {
	Store          *Store
	PubsubName     string
	Topic          string
	Msg            T
	IdempotencyKey string
}

// EnqueueTopicMsg marshals Msg as JSON and enqueues it. Prefer this when setting IdempotencyKey.
func EnqueueTopicMsg[T any](ctx context.Context, in EnqueueTopicInput[T]) error {
	topic := strings.TrimSpace(in.Topic)
	if topic == "" {
		return fmt.Errorf("outbox: empty topic")
	}
	raw, err := json.Marshal(in.Msg)
	if err != nil {
		return fmt.Errorf("outbox: marshal topic %q: %w", topic, err)
	}
	return in.Store.Enqueue(ctx, Message{
		Pubsub:         in.PubsubName,
		Topic:          topic,
		Payload:        raw,
		IdempotencyKey: in.IdempotencyKey,
	})
}
