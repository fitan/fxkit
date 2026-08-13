package outbox

import "time"

// Status values for [OutboxEvent].
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusPublished  = "published"
	StatusFailed     = "failed"
)

// MetadataIdempotencyKey is the event metadata key carrying the
// producer idempotency key. Consumers can use [Inbox.Once] with the same key.
const MetadataIdempotencyKey = "outbox.idempotency_key"

// OutboxEvent is a row in the transactional outbox table. Written inside the same
// DB transaction as business data; relay publishes via Hatchet after commit.
//
// Claiming sets status=processing, LeaseID, LockedAt, and increments Attempts.
// A live relay heartbeats LockedAt while publishing. Another replica may reclaim
// when LockedAt is older than outbox.claim_timeout (crash / lost lease).
// Completion uses compare-and-swap on LeaseID so a stale markFailed cannot
// resurrect a published row. Delivery remains at-least-once on the wire.
//
// IdempotencyKey, when set, is unique: duplicate Enqueue is a no-op unless the
// existing row is failed (then it is reset to pending). The key is attached to
// event metadata for consumer-side dedup ([Inbox]).
type OutboxEvent struct {
	ID             uint64     `gorm:"primaryKey"`
	Pubsub         string     `gorm:"size:128;not null"`
	Topic          string     `gorm:"size:255;not null;index:idx_outbox_poll,priority:2"`
	Payload        []byte     `gorm:"not null"`
	IdempotencyKey *string    `gorm:"size:128;uniqueIndex"`
	Status         string     `gorm:"size:32;not null;index:idx_outbox_poll,priority:1"`
	Attempts       int        `gorm:"not null;default:0"`
	LastError      string     `gorm:"type:text"`
	LeaseID        string     `gorm:"size:64;index"`
	LockedAt       *time.Time `gorm:"index"`
	CreatedAt      time.Time  `gorm:"index:idx_outbox_poll,priority:3"`
	PublishedAt    *time.Time `gorm:"index"`
}

// TableName overrides the default GORM table name.
func (OutboxEvent) TableName() string { return "outbox_events" }
