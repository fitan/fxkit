// Package outbox implements the transactional outbox pattern.
//
// Enqueue outbox rows inside the same [gormx.Client.Transaction] as business writes;
// [Store.Enqueue] and [EnqueueTopic] / [EnqueueTopicMsg] use [gormx.Client.Conn] so they
// join the active tx. After commit, [Relay] polls pending rows and publishes via:
//
//   - Hatchet Events (default when hatchet.enabled && hatchet.outbox_publisher)
//
// Crash recovery (multi-replica): claiming a row sets status=processing, a LeaseID,
// LockedAt, and increments Attempts. The claiming relay heartbeats LockedAt while
// publishing. Another replica may reclaim when LockedAt is older than
// outbox.claim_timeout (default 30s). markPublished / markFailed are compare-and-swap
// on LeaseID so a stale failure cannot resurrect a published row. Delivery remains
// at-least-once on the wire.
//
// Idempotency:
//   - Producer: set [Message.IdempotencyKey] (or [EnqueueTopicInput.IdempotencyKey]) so
//     duplicate Enqueue is a no-op (pending/processing/published) and the key is
//     published as metadata ([MetadataIdempotencyKey]). A failed row with the same
//     key is reset to pending so the producer can retry after max_retries.
//   - Consumer: use [Inbox.Once] with the same key for at-most-once handler application.
//
// [Module] is included in [github.com/fitan/fxkit.Default]; set outbox.enabled=true to start the relay.
package outbox
