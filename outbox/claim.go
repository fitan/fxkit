package outbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// claimParams 配置单次 claimBatch 调用。
type claimParams struct {
	// Limit 是本次最多认领的行数；<=0 时 [claimBatch] 返回错误。
	Limit       int
	StaleBefore time.Time // LockedAt 早于此时间的 processing 行可被重新认领
}

// claimBatch 将最多 limit 条 pending（或陈旧的 processing）行标记为 processing 并返回。
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

// newLeaseID 生成租约凭证。
//
// 容器不可回退到时间戳：lease_id 是 CAS 的凭证，同纳秒内可重复会让
// 「续租/标记」认错行。rand 不可用时宁可直接报错。
func newLeaseID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("outbox: generate lease id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// claimPredicate 是「可认领」条件：pending 且已到重试时间，或租约已过期的 processing。
// PG 与 SQLite 两条路径共用，避免改一处忘另一处。
const claimPredicate = `((status = ? OR (status = ? AND (locked_at IS NULL OR locked_at < ?))) AND (next_attempt_at IS NULL OR next_attempt_at <= ?))`

// claimSetValues 是认领时要写入的列。
//
// 注意：**不在这里递增 attempts**。租约过期后被其他副本重领只是「换人执行」，
// 不是一次业务失败；把它计入重试预算会让 relay 崩溃/发布超时直接把消息打成 failed。
func claimSetValues(now time.Time, leaseID string) map[string]any {
	return map[string]any{
		"status":    StatusProcessing,
		"locked_at": now,
		"lease_id":  leaseID,
	}
}

// markClaimed 把认领结果同步到内存（PG 路径 UPDATE 后不会回填 RETURNING）。
func markClaimed(claimed []OutboxEvent, now time.Time, leaseID string) {
	for i := range claimed {
		claimed[i].Status = StatusProcessing
		locked := now
		claimed[i].LockedAt = &locked
		claimed[i].LeaseID = leaseID
	}
}

// claimSkipLocked 用 SELECT ... FOR UPDATE SKIP LOCKED 认领一批事件并更新为 processing。
// 适用于支持 SKIP LOCKED 的数据库（如 PostgreSQL）。
func claimSkipLocked(ctx context.Context, db *gorm.DB, p claimParams) ([]OutboxEvent, error) {
	now := time.Now().UTC()
	leaseID, err := newLeaseID()
	if err != nil {
		return nil, err
	}
	var claimed []OutboxEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pending []OutboxEvent
		if err := tx.Raw(
			`SELECT * FROM outbox_events WHERE `+claimPredicate+
				` ORDER BY id ASC LIMIT ? FOR UPDATE SKIP LOCKED`,
			StatusPending, StatusProcessing, p.StaleBefore, now, p.Limit,
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
			Updates(claimSetValues(now, leaseID)).Error; err != nil {
			return fmt.Errorf("outbox: claim update: %w", err)
		}
		claimed = pending
		markClaimed(claimed, now, leaseID)
		return nil
	})
	return claimed, err
}

// claimSQLite 用单条带 RETURNING 的子查询 UPDATE 认领事件（SQLite 不支持 SKIP LOCKED）。
func claimSQLite(ctx context.Context, db *gorm.DB, p claimParams) ([]OutboxEvent, error) {
	now := time.Now().UTC()
	leaseID, err := newLeaseID()
	if err != nil {
		return nil, err
	}
	var claimed []OutboxEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// SQLite 不支持 SKIP LOCKED；子查询 UPDATE 在事务内是原子的。
		if err := tx.Raw(
			`UPDATE outbox_events SET status = ?, locked_at = ?, lease_id = ? WHERE id IN (
				SELECT id FROM outbox_events WHERE `+claimPredicate+
				` ORDER BY id ASC LIMIT ?
			) RETURNING *`,
			StatusProcessing, now, leaseID, StatusPending, StatusProcessing, p.StaleBefore, now, p.Limit,
		).Scan(&claimed).Error; err != nil {
			return fmt.Errorf("outbox: sqlite claim: %w", err)
		}
		return nil
	})
	return claimed, err
}
