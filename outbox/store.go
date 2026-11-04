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

// Message 是一条待提交后投递的 outbox 条目。
type Message struct {
	Pubsub  string
	Topic   string
	Payload []byte
	// IdempotencyKey 可选地对生产端的 Enqueue 去重，
	// 并作为投递 metadata 转发（[MetadataIdempotencyKey]）。
	IdempotencyKey string
}

// JSONMessage 是类型安全的泛型消息容器。
type JSONMessage[T any] struct {
	Pubsub         string
	Topic          string
	Payload        T
	IdempotencyKey string
}

// Store 使用 [gormx.Client.Conn] 写入 outbox 行；存在外层事务时会加入该事务。
type Store struct {
	client *gormx.Client
}

// NewStore 返回由 client 支撑的 Store。Enqueue 要求 client 非 nil。
func NewStore(client *gormx.Client) *Store {
	return &Store{client: client}
}

// Enqueue 在 ctx 对应的连接上插入一条 pending outbox 行（ctx 携带 gormx.WithTx 时为同一事务）。
// 当设置了 IdempotencyKey 且该 key 的行已存在时，Enqueue 返回 nil（幂等）。
// 若已存在的行处于 failed，则重置为 pending，以便生产端在 max_retries 之后重试。
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

// EnqueueJSON 把 T 类型的 payload 序列化为 JSON，并插入 pending outbox 行。
// 利用 Go 1.27+ 的泛型方法能力，省去手写 json.Marshal 的样板代码。
func (s *Store) EnqueueJSON[T any](ctx context.Context, msg JSONMessage[T]) error {
	b, err := json.Marshal(msg.Payload)
	if err != nil {
		return fmt.Errorf("outbox: marshal payload: %w", err)
	}
	return s.Enqueue(ctx, Message{
		Pubsub:         msg.Pubsub,
		Topic:          msg.Topic,
		Payload:        b,
		IdempotencyKey: msg.IdempotencyKey,
	})
}

// enqueueIdempotent 插入带 key 的行，仅在已存在的行处于 failed 时才重置它。
// 使用 ON CONFLICT DO NOTHING，避免唯一约束冲突中止 PostgreSQL 事务
// （Create-then-Updates 会中止）。
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

// EnqueueTopicInput 是 [EnqueueTopicMsg] 的结构体形式。
// Topic 是 Hatchet event key。PubsubName 为兼容 DB 而保留（默认 "hatchet"）。
type EnqueueTopicInput[T any] struct {
	Store          *Store
	PubsubName     string
	Topic          string
	Msg            T
	IdempotencyKey string
}

// EnqueueTopicMsg 把 Msg 序列化为 JSON 并入队。需要设置 IdempotencyKey 时优先用它。
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
