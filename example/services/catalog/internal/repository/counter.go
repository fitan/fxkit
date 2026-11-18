package repository

import (
	"context"
	"errors"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"gorm.io/gorm"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

// CounterRepo 封装 actor 计数器的自增与读取。
type CounterRepo struct {
	db *gormx.Client
}

// NewCounterRepo 绑定 DB。
func NewCounterRepo(db *gormx.Client) *CounterRepo {
	return &CounterRepo{db: db}
}

// Get 在记录不存在时返回零值，而不是 NotFound —— 计数器语义下 0 是合法初始值。
func (r *CounterRepo) Get(ctx context.Context, actorID string) (entity.Counter, error) {
	var row entity.Counter
	err := r.db.Conn(ctx).Where("actor_id = ?", actorID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.Counter{}, nil
	}
	if err != nil {
		return entity.Counter{}, fxerrors.Wrap(err)
	}
	return row, nil
}

// Increment 首次调用时创建行，然后累加 delta；演示 Actor 的单行写入。
func (r *CounterRepo) Increment(ctx context.Context, actorID string, delta int64) (int64, error) {
	row := entity.Counter{ActorID: actorID}
	if err := r.db.Conn(ctx).Where("actor_id = ?", actorID).
		FirstOrCreate(&row, entity.Counter{ActorID: actorID}).Error; err != nil {
		return 0, fxerrors.Wrap(err)
	}
	row.Value += delta
	if err := r.db.Conn(ctx).Model(&entity.Counter{}).
		Where("actor_id = ?", actorID).
		Update("value", row.Value).Error; err != nil {
		return 0, fxerrors.Wrap(err)
	}
	return row.Value, nil
}
