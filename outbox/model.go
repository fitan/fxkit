package outbox

import "time"

// [OutboxEvent] 的状态值。
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusPublished  = "published"
	StatusFailed     = "failed"
)

// MetadataIdempotencyKey 是承载生产端幂等 key 的事件 metadata 键。
// 消费端可用相同的 key 调用 [Inbox.Once]。
const MetadataIdempotencyKey = "outbox.idempotency_key"

// OutboxEvent 是事务性 outbox 表中的一行。它与业务数据写入在同一个
// DB 事务内；relay 在提交之后通过 Hatchet 投递。
//
// 认领会设置 status=processing、LeaseID、LockedAt，并递增 Attempts。
// 活跃的 relay 在投递期间心跳刷新 LockedAt。当 LockedAt 早于
// outbox.claim_timeout 时（崩溃 / 租约丢失），另一个副本可以重新认领。
// 完成时基于 LeaseID 做比较并交换，因此陈旧的 markFailed 不会让已投递的行复活。
// 投递在链路上仍是至少一次。
//
// IdempotencyKey 一旦设置即唯一：重复 Enqueue 是空操作，除非已存在的行处于
// failed（此时会被重置为 pending）。该 key 会附加到事件 metadata 上，
// 供消费端去重（[Inbox]）。
type OutboxEvent struct {
	ID             uint64     `gorm:"primaryKey"`
	Pubsub         string     `gorm:"size:128;not null"`
	Topic          string     `gorm:"size:255;not null;index:idx_outbox_poll,priority:2"`
	Payload        []byte     `gorm:"not null"`
	IdempotencyKey *string    `gorm:"size:128;uniqueIndex"`
	Status         string     `gorm:"size:32;not null;index:idx_outbox_poll,priority:1;index:idx_outbox_claim,priority:1"`
	Attempts       int        `gorm:"not null;default:0"`
	LastError      string     `gorm:"type:text"`
	LeaseID        string     `gorm:"size:64;index"`
	LockedAt       *time.Time `gorm:"index:idx_outbox_claim,priority:2"`
	CreatedAt      time.Time  `gorm:"index:idx_outbox_poll,priority:3"`
	PublishedAt    *time.Time `gorm:"index"`
}

// TableName 覆盖 GORM 默认表名。
func (OutboxEvent) TableName() string { return "outbox_events" }
