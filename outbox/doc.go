// Package outbox 实现事务性 outbox 模式。
//
// 在业务写入所在的同一个 [gormx.Client.Transaction] 内入队 outbox 行；
// [Store.Enqueue] 与 [EnqueueTopicMsg] / [EnqueueTopicInput] 使用 [gormx.Client.Conn]，
// 因此会加入当前活跃的事务。提交之后，[Relay] 轮询 pending 行，并通过
// 一个 [EventPublisher] 投递：
//
//   - Hatchet Events（hatchetx，当 hatchet.enabled && hatchet.outbox_publisher 时）
//   - 或用 [ProvidePublisher] 注册的自定义驱动（Kafka、NATS、…）
//
// 崩溃恢复（多副本）：认领一行会设置 status=processing、一个 LeaseID 和
// LockedAt。Attempts 只在真正投递失败时递增，重新认领不计入重试预算。
// 认领该行的 relay 在投递期间心跳刷新 LockedAt。
// 当 LockedAt 早于 outbox.claim_timeout（默认 30s）时，另一个副本可以重新认领。
// markPublished / markFailed 基于 LeaseID 做比较并交换（compare-and-swap），
// 因此陈旧的失败标记不会让已投递的行复活。投递在链路上仍是至少一次。
//
// 幂等：
//   - 生产端：设置 [Message.IdempotencyKey]（或 [EnqueueTopicInput.IdempotencyKey]），
//     使重复 Enqueue 成为空操作（pending/processing/published），并让该 key 作为
//     metadata 投递（[MetadataIdempotencyKey]）。同 key 的 failed 行会被重置为
//     pending，以便生产端在 max_retries 之后重试。
//   - 消费端：使用 [Inbox.Once] 与相同的 key，实现幂等去重（两阶段认领：
//     短事务认领 → 事务外执行 → 标记 done；崩溃后超时可重新认领）。
//
// [Module] 已包含在 [github.com/fitan/fxkit.Default] 中；设置 outbox.enabled=true 即可启动 relay。
package outbox
