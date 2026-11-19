package gormx

import (
	"context"

	"github.com/fitan/fxkit/fxerrors"
)

// Versioned 是乐观锁模型需要实现的接口。
type Versioned interface {
	// GetVersion 返回当前记录的 version 列值。
	GetVersion() int64
	// SetVersion 由 GORM 在查询/更新后写回 version 列。
	SetVersion(int64)
}

// UpdateWithVersion 执行乐观锁更新：
//
//	UPDATE ... SET fields..., version = version + 1 WHERE pk = ? AND version = ?
//
// 冲突（RowsAffected == 0）返回 fxerrors.Conflict。
// 调用方需显式传 fields，避免误更新整行。
func UpdateWithVersion(ctx context.Context, db *Client, model Versioned, fields ...string) error {
	if db == nil || !db.Enabled() {
		return fxerrors.Internal("gormx: UpdateWithVersion requires a configured database")
	}
	if model == nil {
		return fxerrors.Internal("gormx: UpdateWithVersion requires a model")
	}
	if len(fields) == 0 {
		return fxerrors.Internal("gormx: UpdateWithVersion requires fields")
	}
	old := model.GetVersion()
	model.SetVersion(old + 1)
	sel := append([]string{}, fields...)
	sel = append(sel, "version")

	res := db.Conn(ctx).Model(model).Where("version = ?", old).Select(sel).Updates(model)
	if res.Error != nil {
		model.SetVersion(old)
		return fxerrors.Wrap(res.Error)
	}
	if res.RowsAffected == 0 {
		model.SetVersion(old)
		return fxerrors.Conflict("version conflict")
	}
	return nil
}
