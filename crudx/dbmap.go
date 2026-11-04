package crudx

import (
	"errors"
	"strings"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/gorm"
)

// MapDBError 将常见 GORM 错误映射为 fxerrors HTTP 类型。
//
// 对外只返回固定文案，原始数据库错误通过 [Error.WithCause] 保留在错误链中，
// 避免把表名/列名/连接信息等内部细节暴露给客户端。
func MapDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fxerrors.NotFound("entity", "")
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fxerrors.Conflict("duplicate key").WithCause(err)
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "duplicate") || strings.Contains(low, "unique"):
		return fxerrors.Conflict("duplicate key").WithCause(err)
	case strings.Contains(low, "foreign key"):
		return fxerrors.Conflict("foreign key constraint violation").WithCause(err)
	}
	return fxerrors.Wrap(err)
}
