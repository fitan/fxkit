package outbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// claimParams configures a single claimBatch call.
type claimParams struct {
	Limit       int
	StaleBefore time.Time // processing rows with LockedAt before this are reclaimable
}

// claimBatch marks up to limit pending (or stale processing) rows as processing and returns them.
func claimBatch(ctx context.Context, db *gorm.DB, p claimParams) ([]OutboxEvent, error) {
	if p.Limit <= 0 {
		return nil, fmt.Errorf("outbox: claim limit must be > 0")
	}
	switch db.Dialector.Name() {
	case "sqlite":
		return claimSQLite(ctx, db, p)
	default:
		return claimSkipLocked(ctx, db, p)
	}
}

func newLeaseID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func claimSkipLocked(ctx context.Context, db *gorm.DB, p claimParams) ([]OutboxEvent, error) {
	now := time.Now().UTC()
	leaseID := newLeaseID()
	var claimed []OutboxEvent
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pending []OutboxEvent
		if err := tx.Raw(
			`SELECT * FROM outbox_events
			 WHERE status = ?
			    OR (status = ? AND (locked_at IS NULL OR locked_at < ?))
			 ORDER BY id ASC LIMIT ? FOR UPDATE SKIP LOCKED`,
			StatusPending, StatusProcessing, p.StaleBefore, p.Limit,
		).Scan(&pending).Error; err != nil {
			return fmt.Errorf("outbox: claim select: %w", err)
		}
		if len(pending) == 0 {
			return nil
		}
		ids := make([]uint64, len(pending))
		for i := range pending {
			ids[i] = pending[i].ID
		}
		if err := tx.Model(&OutboxEvent{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":    StatusProcessing,
				"locked_at": now,
				"lease_id":  leaseID,
				"attempts":  gorm.Expr("attempts + 1"),
			}).Error; err != nil {
			return fmt.Errorf("outbox: claim update: %w", err)
		}
		claimed = pending
		for i := range claimed {
			claimed[i].Status = StatusProcessing
			locked := now
			claimed[i].LockedAt = &locked
			claimed[i].LeaseID = leaseID
			claimed[i].Attempts++
		}
		return nil
	})
	return claimed, err
}

func claimSQLite(ctx context.Context, db *gorm.DB, p claimParams) ([]OutboxEvent, error) {
	now := time.Now().UTC()
	leaseID := newLeaseID()
	var claimed []OutboxEvent
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// SQLite lacks SKIP LOCKED; subquery UPDATE is atomic within the transaction.
		if err := tx.Raw(
			`UPDATE outbox_events SET status = ?, locked_at = ?, lease_id = ?, attempts = attempts + 1 WHERE id IN (
				SELECT id FROM outbox_events
				WHERE status = ?
				   OR (status = ? AND (locked_at IS NULL OR locked_at < ?))
				ORDER BY id ASC LIMIT ?
			) RETURNING *`,
			StatusProcessing, now, leaseID, StatusPending, StatusProcessing, p.StaleBefore, p.Limit,
		).Scan(&claimed).Error; err != nil {
			return fmt.Errorf("outbox: sqlite claim: %w", err)
		}
		return nil
	})
	return claimed, err
}
