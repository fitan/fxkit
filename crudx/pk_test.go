package crudx

import (
	"context"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type pkTestModel struct {
	ID   int64  `gorm:"primaryKey"`
	Name string `gorm:"size:64"`
}

func TestResolveModel_and_FirstByID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&pkTestModel{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&pkTestModel{ID: 7, Name: "alice"}).Error; err != nil {
		t.Fatal(err)
	}

	meta, err := ResolveModel[pkTestModel](db)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Resource != "pkTestModel" || meta.Table != "pk_test_models" || meta.Column != "id" {
		t.Fatalf("meta = %+v", meta)
	}

	got, err := FirstByID[pkTestModel](context.Background(), db, "7")
	if err != nil || got.Name != "alice" {
		t.Fatalf("FirstByID: got=%+v err=%v", got, err)
	}

	_, err = FirstByID[pkTestModel](context.Background(), db, "404")
	if !fxerrors.Is(err, fxerrors.KindNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}
