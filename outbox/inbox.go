package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fitan/fxkit/gormx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrInboxBusy 表示这个 key 正被别的投递处理，本次没有执行 Fn。
//
// 消费端应把该错误交回 broker 重试，不要当成成功 ack。done 仍然返回 nil。
// 若 Fn 已成功但标记 done 失败，租约过期前的重试也会拿到这个错误；
// 过期后会再执行一次 Fn，所以 Fn 仍要能重入。
var ErrInboxBusy = errors.New("outbox: inbox key is being processed")

// Inbox 记录的消费状态。
const (
	InboxProcessing = "processing"
	InboxDone       = "done"
)

const defaultInboxClaimTimeout = 5 * time.Minute

// InboxEvent 记录一条消费端幂等 key 的认领/完成状态。
//
// 两阶段语义：
//   - 先插入 processing（或 CAS 抢占过期的 processing）——短事务；
//   - 在事务外执行 handler；
//   - 成功后标记 done；失败则删除认领行，让下一次投递可以重试。
//
// ProcessedAt 在认领时写入，完成时更新为完成时间（保留非空约束，兼容既有表结构）。
type InboxEvent struct {
	ID          string     `gorm:"primaryKey;size:128"`
	Topic       string     `gorm:"size:255;not null"`
	Status      string     `gorm:"size:16;not null;default:done;index:idx_outbox_inbox_status,priority:1"`
	ClaimedAt   *time.Time `gorm:"index:idx_outbox_inbox_status,priority:2"`
	LeaseID     string     `gorm:"size:64"`
	ProcessedAt time.Time  `gorm:"not null"`
}

// TableName 覆盖 GORM 默认表名。
func (InboxEvent) TableName() string { return "outbox_inbox_events" }

// Inbox 提供消费端的幂等处理辅助，以生产端的 [Message.IdempotencyKey] 为键。
type Inbox struct {
	client       *gormx.Client
	claimTimeout time.Duration
}

// NewInbox 返回由 client 支撑的 Inbox（认领租约 5m）。
func NewInbox(client *gormx.Client) *Inbox {
	return NewInboxWithTimeout(client, defaultInboxClaimTimeout)
}

// NewInboxWithTimeout 指定认领租约时长；<=0 使用默认 5m。
func NewInboxWithTimeout(client *gormx.Client, claimTimeout time.Duration) *Inbox {
	if claimTimeout <= 0 {
		claimTimeout = defaultInboxClaimTimeout
	}
	return &Inbox{client: client, claimTimeout: claimTimeout}
}

// Migrate 创建/补齐 inbox 表。outbox 启用时由 [Module] 调用。
func (in *Inbox) Migrate(ctx context.Context) error {
	if in == nil || in.client == nil || !in.client.Enabled() {
		return fmt.Errorf("outbox: inbox has no configured database")
	}
	return in.client.Conn(ctx).AutoMigrate(&InboxEvent{})
}

// OnceInput 是 [Inbox.Once] 的参数结构体。
type OnceInput struct {
	Key   string
	Topic string
	Fn    func(context.Context) error
}

// Once 对 key 最多成功一次：key 为空时总是执行 Fn（不做去重）。
//
// 先认领，再在事务外执行 Fn，最后标记 done。长 handler 不占着行锁；
// 崩溃后认领超时可被重新认领。语义是「至少一次投递 + 幂等键去重」，Fn 应可重入。
//
// 已经 done 时返回 nil。未过期的 processing 返回 [ErrInboxBusy]，调用方应重试而不是 ack。
// 对方刚释放认领（行被删掉）时会再认领一次，仍然抢不到则同样返回 [ErrInboxBusy]。
func (in *Inbox) Once(ctx context.Context, inp OnceInput) error {
	if in == nil || in.client == nil || !in.client.Enabled() {
		return fmt.Errorf("outbox: inbox has no configured database")
	}
	key := strings.TrimSpace(inp.Key)
	if key == "" {
		if inp.Fn == nil {
			return nil
		}
		return inp.Fn(ctx)
	}
	if inp.Fn == nil {
		return fmt.Errorf("outbox: inbox Once requires Fn")
	}

	lease, err := in.claim(ctx, key, strings.TrimSpace(inp.Topic))
	if err != nil || lease == "" {
		return err
	}

	if err := inp.Fn(ctx); err != nil {
		// 状态回写脱离调用方取消：Hatchet 超时或停机时 ctx 已经取消，
		// 若 release 跟着失败，行会停在 processing，重试在租约内全是 ErrInboxBusy。
		relCtx, cancel := terminalWriteContext(ctx)
		relErr := in.release(relCtx, key, lease)
		cancel()
		if relErr != nil {
			slog.WarnContext(ctx, "outbox: inbox release failed; will rely on claim timeout",
				"key", key, "error", relErr)
		}
		return err
	}
	doneCtx, cancel := terminalWriteContext(ctx)
	defer cancel()
	return in.markDone(doneCtx, key, lease)
}

// claim 认领 key。返回的 lease 为空且 err 为 nil 表示已经 done。
// 未过期的 processing 返回 [ErrInboxBusy]。
func (in *Inbox) claim(ctx context.Context, key, topic string) (string, error) {
	for range 2 {
		lease, err := newLeaseID()
		if err != nil {
			return "", err
		}
		claimed, err := in.tryClaim(ctx, key, topic, lease)
		if err != nil {
			return "", err
		}
		if claimed {
			return lease, nil
		}
		state, err := in.lookup(ctx, key)
		if err != nil {
			return "", err
		}
		switch state {
		case inboxMissing:
			// 认领冲突之后行被 release 删掉了，再试一次插入。
			continue
		case inboxDone:
			return "", nil
		default:
			return "", ErrInboxBusy
		}
	}
	return "", ErrInboxBusy
}

func (in *Inbox) lookup(ctx context.Context, key string) (inboxState, error) {
	var row InboxEvent
	err := in.client.Conn(ctx).Where("id = ?", key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return inboxMissing, nil
	}
	if err != nil {
		return 0, err
	}
	if row.Status == InboxDone {
		return inboxDone, nil
	}
	return inboxBusy, nil
}

type inboxState int

const (
	inboxMissing inboxState = iota
	inboxDone
	inboxBusy
)

// tryClaim 认领 key。返回 false 时调用方要再读状态，区分 done、处理中和刚被删掉。
func (in *Inbox) tryClaim(ctx context.Context, key, topic, lease string) (bool, error) {
	now := time.Now().UTC()
	row := InboxEvent{
		ID:          key,
		Topic:       topic,
		Status:      InboxProcessing,
		ClaimedAt:   &now,
		LeaseID:     lease,
		ProcessedAt: now,
	}
	res := in.client.Conn(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		return true, nil
	}

	// 已存在：只有「过期的 processing」可以抢占；done 直接跳过。
	cutoff := now.Add(-in.claimTimeout)
	cas := in.client.Conn(ctx).Model(&InboxEvent{}).
		Where("id = ? AND status = ? AND (claimed_at IS NULL OR claimed_at < ?)", key, InboxProcessing, cutoff).
		Updates(map[string]any{
			"claimed_at": now,
			"lease_id":   lease,
			"topic":      topic,
		})
	if cas.Error != nil {
		return false, cas.Error
	}
	return cas.RowsAffected > 0, nil
}

func (in *Inbox) markDone(ctx context.Context, key, lease string) error {
	res := in.client.Conn(ctx).Model(&InboxEvent{}).
		Where("id = ? AND lease_id = ? AND status = ?", key, lease, InboxProcessing).
		Updates(map[string]any{
			"status":       InboxDone,
			"processed_at": time.Now().UTC(),
			"claimed_at":   nil,
			"lease_id":     "",
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("outbox: inbox mark done lost claim for %q", key)
	}
	return nil
}

func (in *Inbox) release(ctx context.Context, key, lease string) error {
	return in.client.Conn(ctx).
		Where("id = ? AND lease_id = ? AND status = ?", key, lease, InboxProcessing).
		Delete(&InboxEvent{}).Error
}

const inboxPurgeBatchSize = 1000

// PurgeDone 批量删除 processed_at 早于 before 的 done 行。
// 默认不自动清理；开启 outbox.purge_interval 后由 purge loop 调用。
func (in *Inbox) PurgeDone(ctx context.Context, before time.Time) (int64, error) {
	if in == nil || in.client == nil || !in.client.Enabled() {
		return 0, fmt.Errorf("outbox: inbox has no configured database")
	}
	if before.IsZero() {
		return 0, fmt.Errorf("outbox: inbox cutoff is required")
	}
	var total int64
	for {
		var ids []string
		if err := in.client.Conn(ctx).Model(&InboxEvent{}).
			Where("status = ? AND processed_at < ?", InboxDone, before).
			Order("id").Limit(inboxPurgeBatchSize).
			Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		res := in.client.Conn(ctx).Where("id IN ?", ids).Delete(&InboxEvent{})
		total += res.RowsAffected
		if res.Error != nil {
			return total, res.Error
		}
		if len(ids) < inboxPurgeBatchSize {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// ListStale 返回 claimed_at 早于 before 的 processing 行，供排查卡住的消费。
func (in *Inbox) ListStale(ctx context.Context, before time.Time, limit int) ([]InboxEvent, error) {
	if in == nil || in.client == nil || !in.client.Enabled() {
		return nil, fmt.Errorf("outbox: inbox has no configured database")
	}
	if before.IsZero() {
		return nil, fmt.Errorf("outbox: inbox cutoff is required")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var rows []InboxEvent
	if err := in.client.Conn(ctx).
		Where("status = ? AND claimed_at IS NOT NULL AND claimed_at < ?", InboxProcessing, before).
		Order("claimed_at").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
