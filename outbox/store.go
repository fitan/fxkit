package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fitan/fxkit/gormx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Message 是一条待提交后投递的 outbox 条目。
type Message struct {
	// Pubsub 是投递后端名；为空时 [Store.Enqueue] 默认 "hatchet"。
	Pubsub string
	// Topic 是投递目标（Hatchet event key）；为空时 Enqueue 返回错误。
	Topic string
	// Payload 是消息体；为空时 Enqueue 返回错误。
	Payload []byte
	// IdempotencyKey 可选地对生产端的 Enqueue 去重，
	// 并作为投递 metadata 转发（[MetadataIdempotencyKey]）。
	IdempotencyKey string
}

// Store 使用 [gormx.Client.Conn] 写入 outbox 行；存在外层事务时会加入该事务。
type Store struct {
	// client 是 outbox 表所在的数据库客户端。
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
	if s == nil || s.client == nil || !s.client.Enabled() {
		return fmt.Errorf("outbox: store has no configured database")
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

// PurgePublished 批量删除 published_at 早于 cutoff 的已发布行，返回删除行数。
//
// 默认不自动清理；开启 outbox.purge_interval 后由 purge loop 调用。
// 只删 published；pending/processing/failed 永远不会被这里删除。
func (s *Store) PurgePublished(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return 0, fmt.Errorf("outbox: store has no configured database")
	}
	if cutoff.IsZero() {
		return 0, fmt.Errorf("outbox: cutoff is required")
	}
	return s.purgeWhere(ctx, "status = ? AND published_at IS NOT NULL AND published_at < ?", StatusPublished, cutoff)
}

// PurgeFailed 批量删除 created_at 早于 cutoff 的 failed 行。
// 用于重试预算耗尽后的保留策略；默认不自动清理。
func (s *Store) PurgeFailed(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return 0, fmt.Errorf("outbox: store has no configured database")
	}
	if cutoff.IsZero() {
		return 0, fmt.Errorf("outbox: cutoff is required")
	}
	return s.purgeWhere(ctx, "status = ? AND created_at < ?", StatusFailed, cutoff)
}

// ListFailed 返回 failed 行，按 id 升序，供 SDK/CLI 排查与重放。
func (s *Store) ListFailed(ctx context.Context, limit int) ([]OutboxEvent, error) {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return nil, fmt.Errorf("outbox: store has no configured database")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var rows []OutboxEvent
	if err := s.client.Conn(ctx).Where("status = ?", StatusFailed).Order("id").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// purgeBatchSize 是 [Store.purgeWhere] 每批删除的行数。
const purgeBatchSize = 1000

// purgeWhere 按 id 分批删除，避免一次 DELETE 锁住大范围行（Postgres 不支持 DELETE LIMIT）。
func (s *Store) purgeWhere(ctx context.Context, where string, args ...any) (int64, error) {
	var total int64
	for {
		var ids []uint64
		if err := s.client.Conn(ctx).Model(&OutboxEvent{}).
			Where(where, args...).
			Order("id").Limit(purgeBatchSize).
			Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		res := s.client.Conn(ctx).Where("id IN ?", ids).Delete(&OutboxEvent{})
		total += res.RowsAffected
		if res.Error != nil {
			return total, res.Error
		}
		if len(ids) < purgeBatchSize {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// RequeueFilter 选择要重新入队的 failed 行。零值表示全部 failed 行。
type RequeueFilter struct {
	Topics []string  // 仅处理这些 topic；空 = 全部
	Before time.Time // 仅处理 created_at 早于该时间的行；零值 = 不限
	Limit  int       // 最多重放条数；<=0 = 不限
}

// RequeueFailed 把 failed 行重置为 pending，用于重试预算耗尽/永久错误修复后的重放。
//
// 重置内容：attempts=0、next_attempt_at=NULL、last_error 清空、释放租约。
// 返回实际重置的行数。
func (s *Store) RequeueFailed(ctx context.Context, f RequeueFilter) (int64, error) {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return 0, fmt.Errorf("outbox: store has no configured database")
	}
	sub := s.client.Conn(ctx).Model(&OutboxEvent{}).Where("status = ?", StatusFailed)
	if len(f.Topics) > 0 {
		sub = sub.Where("topic IN ?", f.Topics)
	}
	if !f.Before.IsZero() {
		sub = sub.Where("created_at < ?", f.Before)
	}
	if f.Limit > 0 {
		sub = sub.Limit(f.Limit)
	}
	// 先取 ID 再更新：MySQL 不支持 `IN (SELECT ... LIMIT ?)`。
	var ids []uint64
	if err := sub.Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res := s.client.Conn(ctx).Model(&OutboxEvent{}).
		Where("id IN ?", ids).
		Updates(map[string]any{
			"status":          StatusPending,
			"attempts":        0,
			"next_attempt_at": nil,
			"last_error":      "",
			"locked_at":       nil,
			"lease_id":        "",
		})
	return res.RowsAffected, res.Error
}

// EnqueueTopicInput 是 [EnqueueTopicMsg] 的结构体形式。
// Topic 是 Hatchet event key。PubsubName 为兼容 DB 而保留（默认 "hatchet"）。
type EnqueueTopicInput[T any] struct {
	// Store 是写入 outbox 的存储；必须非 nil。
	Store *Store
	// PubsubName 为兼容 DB 而保留；空时由 [Store.Enqueue] 默认 "hatchet"。
	PubsubName string
	// Topic 是 Hatchet event key；入队前会去掉首尾空白。
	Topic string
	// Msg 会被序列化为 JSON 作为 payload。
	Msg T
	// IdempotencyKey 可选，用于生产端去重并作为 metadata 转发给消费端。
	IdempotencyKey string
}

// EnqueueTopicMsg 把 Msg 序列化为 JSON 并入队。需要设置 IdempotencyKey 时优先用它。
func EnqueueTopicMsg[T any](ctx context.Context, in EnqueueTopicInput[T]) error {
	raw, err := json.Marshal(in.Msg)
	if err != nil {
		return fmt.Errorf("outbox: marshal topic %q: %w", strings.TrimSpace(in.Topic), err)
	}
	// topic / payload 的校验统一交给 Enqueue，避免两套错误文案与两处维护。
	return in.Store.Enqueue(ctx, Message{
		Pubsub:         in.PubsubName,
		Topic:          strings.TrimSpace(in.Topic),
		Payload:        raw,
		IdempotencyKey: in.IdempotencyKey,
	})
}
