package gormx_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
)

type versionedRow struct {
	ID      int64  `gorm:"primaryKey"`
	Name    string `gorm:"size:64"`
	Version int64  `gorm:"not null;default:1"`
}

func (r *versionedRow) GetVersion() int64  { return r.Version }
func (r *versionedRow) SetVersion(v int64) { r.Version = v }

func TestUpdateWithVersion_Conflict(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "opt.db")
	client, err := gormx.New(&gormx.Config{Driver: "sqlite", DSN: dsn, LogLevel: "silent"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.Conn(ctx).AutoMigrate(&versionedRow{}); err != nil {
		t.Fatal(err)
	}
	row := versionedRow{Name: "a", Version: 1}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	first := versionedRow{ID: row.ID, Name: "first", Version: 1}
	if err := gormx.UpdateWithVersion(ctx, client, &first, "name"); err != nil {
		t.Fatal(err)
	}
	if first.Version != 2 {
		t.Fatalf("version=%d, want 2", first.Version)
	}

	second := versionedRow{ID: row.ID, Name: "second", Version: 1}
	err = gormx.UpdateWithVersion(ctx, client, &second, "name")
	if err == nil {
		t.Fatal("expected conflict")
	}
	var fe *fxerrors.Error
	if !errors.As(err, &fe) || fe.Kind != fxerrors.KindConflict {
		t.Fatalf("err=%v, want conflict", err)
	}

	var got versionedRow
	if err := client.Conn(ctx).First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Name != "first" || got.Version != 2 {
		t.Fatalf("db=%+v, want first/2", got)
	}
}
