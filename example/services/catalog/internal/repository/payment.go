package repository

import (
	"context"

	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/gormx"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

// PaymentRepo 汇总 payments 表的读写。
//
// 放在共享 repository 包而不是 payments feature 内：对账、报表等其它 feature
// 可以直接复用同一套 SQL，而不必 import payments 造成 import cycle。
type PaymentRepo struct {
	db *gormx.Client
}

// NewPaymentRepo 绑定 DB。
func NewPaymentRepo(db *gormx.Client) *PaymentRepo {
	return &PaymentRepo{db: db}
}

// Insert 写入支付单；唯一约束冲突由 crudx.MapDBError 映射。
func (r *PaymentRepo) Insert(ctx context.Context, p *entity.Payment) error {
	return crudx.MapDBError(r.db.Conn(ctx).Create(p).Error)
}

// InsertRefund 写入一条退款记录。
func (r *PaymentRepo) InsertRefund(ctx context.Context, ref *entity.Refund) error {
	return crudx.MapDBError(r.db.Conn(ctx).Create(ref).Error)
}

// ListRefunds 按创建时间返回支付单的退款记录。
func (r *PaymentRepo) ListRefunds(ctx context.Context, paymentID string) ([]entity.Refund, error) {
	var rows []entity.Refund
	if err := r.db.Conn(ctx).Where("payment_id = ?", paymentID).Order("created_at").Find(&rows).Error; err != nil {
		return nil, crudx.MapDBError(err)
	}
	return rows, nil
}

// Get 按主键读取，不加租户条件：webhook 等无租户上下文的回调也要能定位支付单。
func (r *PaymentRepo) Get(ctx context.Context, id string) (entity.Payment, error) {
	row, err := crudx.FirstByID[entity.Payment](ctx, r.db.Conn(ctx), id)
	if err != nil {
		return entity.Payment{}, err
	}
	return *row, nil
}

// ListRecent 返回最近的支付单，按创建时间倒序。
func (r *PaymentRepo) ListRecent(ctx context.Context, limit int) ([]entity.Payment, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var rows []entity.Payment
	if err := r.db.Conn(ctx).Order("created_at desc, id desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, crudx.MapDBError(err)
	}
	return rows, nil
}

// ListSettled 返回已 capture 的支付单，用于对账的内部侧。
func (r *PaymentRepo) ListSettled(ctx context.Context) ([]entity.Payment, error) {
	q := r.db.Conn(ctx).Model(&entity.Payment{}).Where("state = ?", "captured")
	var rows []entity.Payment
	if err := q.Order("created_at").Find(&rows).Error; err != nil {
		return nil, crudx.MapDBError(err)
	}
	return rows, nil
}
