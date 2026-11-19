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
	// InboxProcessing 表示该 key 正被某个投递认领处理。
	InboxProcessing = "processing"
	// InboxDone 表示该 key 已处理完成，后续投递直接跳过。
	InboxDone = "done"
)

// defaultInboxClaimTimeout 是 [NewInbox] 使用的认领租约默认值。
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
	// ID 是幂等 key（通常取自 [Message.IdempotencyKey]），也是主键。
	ID string `gorm:"primaryKey;size:128"`
	// Topic 是产生该事件的主题，仅用于排查。
	Topic string `gorm:"size:255;not null"`
	// Status 取 [InboxProcessing] 或 [InboxDone]，默认 done；与 ClaimedAt 组成索引。
	Status string `gorm:"size:16;not null;default:done;index:idx_outbox_inbox_status,priority:1"`
	// ClaimedAt 是本次认领时间；标记 done 时置为 NULL，失败释放则删除整行。
	// 与 Status 组成索引，供过期抢占与 [Inbox.ListStale] 查询。
	ClaimedAt *time.Time `gorm:"index:idx_outbox_inbox_status,priority:2"`
	// LeaseID 是认领租约标识，标记完成/释放时用作 CAS 条件，避免陈旧写入。
	LeaseID string `gorm:"size:64"`
	// ProcessedAt 认领时写入，完成时更新为完成时间；保留 NOT NULL 以兼容既有表结构。
	// [Inbox.PurgeDone] 按它判定可清理的 done 行。
	ProcessedAt time.Time `gorm:"not null"`
}

// TableName 覆盖 GORM 默认表名。
func (InboxEvent) TableName() string { return "outbox_inbox_events" }

// Inbox 提供消费端的幂等处理辅助，以生产端的 [Message.IdempotencyKey] 为键。
type Inbox struct {
	// client 是 inbox 表所在的数据库客户端。
	client *gormx.Client
	// claimTimeout 是 processing 行的认领租约时长，过期后可被重新认领。
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
	// Key 是幂等 key；为空时直接执行 Fn，不做去重。
	Key string
	// Topic 记录到 [InboxEvent.Topic]，仅用于排查。
	Topic string
	// Fn 是实际处理逻辑，会被重入（如认领租约过期后再次执行），必须幂等。
	Fn func(context.Context) error
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

	start := time.Now()

	lease, err := in.claim(ctx, key, strings.TrimSpace(inp.Topic))
	if err != nil {
		recordInbox(ctx, inboxResult(err), time.Since(start))
		return err
	}
	if lease == "" {
		// 已经 done：本次是幂等重放，Fn 没有执行。
		recordInbox(ctx, "skipped", time.Since(start))
		return nil
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
		recordInbox(ctx, inboxResult(err), time.Since(start))
		return err
	}
	doneCtx, cancel := terminalWriteContext(ctx)
	defer cancel()
	err = in.markDone(doneCtx, key, lease)
	recordInbox(ctx, inboxResult(err), time.Since(start))
	return err
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

// lookup 读取 key 的当前状态：不存在、已 done 或处理中。
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

// inboxState 是 [Inbox.claim] 读到的 key 状态。
type inboxState int

const (
	// inboxMissing 表示 key 不存在（可直接插入认领）。
	inboxMissing inboxState = iota
	// inboxDone 表示 key 已处理完成。
	inboxDone
	// inboxBusy 表示 key 正被其他投递持有且租约未过期。
	inboxBusy
)

// inboxResult 把 [Inbox.Once] 的返回错误映射为指标标签。
func inboxResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInboxBusy):
		return "busy"
	default:
		return "error"
	}
}

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

// markDone 以 lease 为 CAS 条件把认领行标记为 done；未命中（租约已丢失）时返回错误。
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

// release 以 lease 为 CAS 条件删除认领行，让失败的消息可被重新投递。
func (in *Inbox) release(ctx context.Context, key, lease string) error {
	return in.client.Conn(ctx).
		Where("id = ? AND lease_id = ? AND status = ?", key, lease, InboxProcessing).
		Delete(&InboxEvent{}).Error
}

// inboxPurgeBatchSize 是 [Inbox.PurgeDone] 每批删除的行数。
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
