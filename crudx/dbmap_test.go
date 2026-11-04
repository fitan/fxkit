package crudx

import (
	"errors"
	"strings"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/gorm"
)

func conflictMessage(t *testing.T, err error) string {
	t.Helper()
	if !fxerrors.Is(err, fxerrors.KindConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	var fe *fxerrors.Error
	if !errors.As(err, &fe) {
		t.Fatalf("want fxerrors.Error, got %T", err)
	}
	return fe.Message
}

func TestMapDBError_duplicateHidesRawDBText(t *testing.T) {
	raw := errors.New("UNIQUE constraint failed: users.email")
	err := MapDBError(raw)
	if msg := conflictMessage(t, err); msg != "duplicate key" {
		t.Fatalf("message=%q", msg)
	}
	if strings.Contains(err.Error(), "users.email") {
		t.Fatalf("raw db detail leaked: %v", err)
	}
	// cause 保留在错误链里，方便日志排查。
	if !errors.Is(err, raw) {
		t.Fatal("cause was not preserved")
	}
}

func TestMapDBError_gormDuplicatedKey(t *testing.T) {
	err := MapDBError(gorm.ErrDuplicatedKey)
	if msg := conflictMessage(t, err); msg != "duplicate key" {
		t.Fatalf("message=%q", msg)
	}
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatal("cause was not preserved")
	}
}

func TestMapDBError_foreignKey(t *testing.T) {
	raw := errors.New("FOREIGN KEY constraint failed")
	err := MapDBError(raw)
	if msg := conflictMessage(t, err); msg != "foreign key constraint violation" {
		t.Fatalf("message=%q", msg)
	}
	if !errors.Is(err, raw) {
		t.Fatal("cause was not preserved")
	}
}

func TestMapDBError_notFoundAndNil(t *testing.T) {
	if err := MapDBError(nil); err != nil {
		t.Fatalf("nil: %v", err)
	}
	if !fxerrors.Is(MapDBError(gorm.ErrRecordNotFound), fxerrors.KindNotFound) {
		t.Fatal("want not found")
	}
}
