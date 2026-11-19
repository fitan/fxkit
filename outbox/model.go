package outbox

import "time"

// [OutboxEvent] 的状态值。
const (
	// StatusPending 表示已入队、等待 relay 投递。
	StatusPending = "pending"
	// StatusProcessing 表示已被某个 relay 副本认领、正在投递。
	StatusProcessing = "processing"
	// StatusPublished 表示投递成功。
	StatusPublished = "published"
	// StatusFailed 表示重试预算耗尽，不再自动重试（可用 [Store.RequeueFailed] 重放）。
	StatusFailed = "failed"
)

// MetadataIdempotencyKey 是承载生产端幂等 key 的事件 metadata 键。
// 消费端可用相同的 key 调用 [Inbox.Once]。
const MetadataIdempotencyKey = "outbox.idempotency_key"

// OutboxEvent 是事务性 outbox 表中的一行。它与业务数据写入在同一个
// DB 事务内；relay 在提交之后通过 Hatchet 投递。
//
// 认领会设置 status=processing、LeaseID、LockedAt；Attempts 只在**真实投递失败**时
// 递增（租约过期被其他副本重领不计入重试预算）。活跃的 relay 在投递期间心跳刷新
// LockedAt。当 LockedAt 早于 outbox.claim_timeout 时（崩溃 / 租约丢失），另一个副本
// 可以重新认领。完成时基于 LeaseID 做比较并交换，因此陈旧的 markFailed 不会让
// 已投递的行复活。投递在链路上仍是至少一次。
//
// IdempotencyKey 一旦设置即唯一：重复 Enqueue 是空操作，除非已存在的行处于
// failed（此时会被重置为 pending）。该 key 会附加到事件 metadata 上，
// 供消费端去重（[Inbox]）。
type OutboxEvent struct {
	// ID 是自增主键；relay 按 id 升序分批认领。
	ID uint64 `gorm:"primaryKey"`
	// Pubsub 是投递后端名；[Store.Enqueue] 未指定时默认 "hatchet"。
	Pubsub string `gorm:"size:128;not null"`
	// Topic 是投递目标（Hatchet event key），与 Status、CreatedAt 组成轮询索引 idx_outbox_poll。
	Topic string `gorm:"size:255;not null;index:idx_outbox_poll,priority:2"`
	// Payload 是待投递的原始字节（通常是 JSON）。
	Payload []byte `gorm:"not null"`
	// IdempotencyKey 可选且唯一：重复 Enqueue 幂等，已存在的 failed 行会被重置为 pending。
	IdempotencyKey *string `gorm:"size:128;uniqueIndex"`
	// Status 取 [StatusPending]、[StatusProcessing]、[StatusPublished] 或 [StatusFailed]。
	Status string `gorm:"size:32;not null;index:idx_outbox_poll,priority:1;index:idx_outbox_claim,priority:1;index:idx_outbox_retry,priority:1"`
	// Attempts 只在真实投递失败时递增（租约过期重领不计入）；
	// 达到 outbox.max_retries 后置为 failed（0 表示首次失败即 failed）。
	Attempts int `gorm:"not null;default:0"`
	// LastError 是最近一次投递失败原因；发布成功或重放时清空。
	LastError string `gorm:"type:text"`
	// LeaseID 是认领租约标识，markPublished/markFailed 按它做 CAS，避免陈旧写入。
	LeaseID string `gorm:"size:64;index"`
	// LockedAt 是最近一次认领/心跳时间；早于 outbox.claim_timeout 时可被其他副本重新认领。
	LockedAt *time.Time `gorm:"index:idx_outbox_claim,priority:2"`
	// NextAttemptAt 是下次可重试时间；pending 且未到期时不会被认领。
	// failed 行为 NULL。
	NextAttemptAt *time.Time `gorm:"index:idx_outbox_retry,priority:2"`
	// CreatedAt 是入队时间，参与轮询索引与 [Store.PurgeFailed]、[Store.RequeueFailed] 的时间过滤。
	CreatedAt time.Time `gorm:"index:idx_outbox_poll,priority:3"`
	// PublishedAt 是投递成功时间；未发布为 NULL，[Store.PurgePublished] 按它清理。
	PublishedAt *time.Time `gorm:"index"`
}

// TableName 覆盖 GORM 默认表名。
func (OutboxEvent) TableName() string { return "outbox_events" }
