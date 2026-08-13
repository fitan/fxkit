package crudx

import (
	"errors"
	"strings"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/gorm"
)

// MapDBError 将常见 GORM 错误映射为 fxerrors HTTP 类型。
func MapDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fxerrors.NotFound("entity", "")
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fxerrors.Conflict("duplicate key")
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "duplicate") || strings.Contains(low, "unique"):
		return fxerrors.Conflict("%s", msg)
	case strings.Contains(low, "foreign key"):
		return fxerrors.Conflict("%s", msg)
	}
	return fxerrors.Wrap(err)
}
