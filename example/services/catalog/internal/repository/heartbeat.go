package repository

import (
	"context"
	"errors"
	"time"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"gorm.io/gorm"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

// HeartbeatRepo 记录提醒器（reminder）的 tick 状态。
type HeartbeatRepo struct {
	db *gormx.Client
}

// NewHeartbeatRepo 绑定 DB。
func NewHeartbeatRepo(db *gormx.Client) *HeartbeatRepo {
	return &HeartbeatRepo{db: db}
}

// Get 在记录不存在时返回零值，由调用方决定如何呈现「尚未 tick」。
func (r *HeartbeatRepo) Get(ctx context.Context, actorID string) (entity.Heartbeat, error) {
	var row entity.Heartbeat
	err := r.db.Conn(ctx).Where("actor_id = ?", actorID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.Heartbeat{}, nil
	}
	if err != nil {
		return entity.Heartbeat{}, fxerrors.Wrap(err)
	}
	return row, nil
}

// Tick 首次调用时创建行，随后 ticks+1 并更新 last_tick。
func (r *HeartbeatRepo) Tick(ctx context.Context, actorID string, now time.Time) (entity.Heartbeat, error) {
	row := entity.Heartbeat{ActorID: actorID}
	if err := r.db.Conn(ctx).Where("actor_id = ?", actorID).
		Attrs(entity.Heartbeat{ActorID: actorID, LastTick: now}).
		FirstOrCreate(&row).Error; err != nil {
		return entity.Heartbeat{}, fxerrors.Wrap(err)
	}
	row.Ticks++
	row.LastTick = now
	if err := r.db.Conn(ctx).Save(&row).Error; err != nil {
		return entity.Heartbeat{}, fxerrors.Wrap(err)
	}
	return row, nil
}
