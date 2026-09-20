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

type pkJoinProfile struct {
	ID     int64 `gorm:"primaryKey"`
	UserID int64
	Bio    string
}

func TestFirstByID_JoinDisambiguatesPK(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&pkTestModel{}, &pkJoinProfile{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&pkTestModel{ID: 7, Name: "alice"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&pkJoinProfile{ID: 1, UserID: 7, Bio: "hi"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveModel[pkTestModel](db); err != nil {
		t.Fatal(err)
	}
	joined := db.Joins("JOIN pk_join_profiles ON pk_join_profiles.user_id = pk_test_models.id")
	got, err := FirstByID[pkTestModel](context.Background(), joined, "7")
	if err != nil || got.Name != "alice" {
		t.Fatalf("FirstByID join: got=%+v err=%v", got, err)
	}
}

type pkEmbeddedGormModel struct {
	gorm.Model
	Title string
}

func TestEmbeddedPrimaryKeyModel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&pkEmbeddedGormModel{}); err != nil {
		t.Fatal(err)
	}

	meta, err := ResolveModel[pkEmbeddedGormModel](db)
	if err != nil {
		t.Fatalf("ResolveModel failed: %v", err)
	}
	if meta.Column != "id" {
		t.Fatalf("expected column id, got %s", meta.Column)
	}

	item := pkEmbeddedGormModel{
		Model: gorm.Model{ID: 42},
		Title: "Test Article",
	}

	// 1. FormatPrimaryKey
	pkStr, err := FormatPrimaryKey(&item)
	if err != nil {
		t.Fatalf("FormatPrimaryKey error: %v", err)
	}
	if pkStr != "42" {
		t.Fatalf("expected '42', got %s", pkStr)
	}

	// 2. CopyPrimaryKey
	var dst pkEmbeddedGormModel
	if err := CopyPrimaryKey(&item, &dst); err != nil {
		t.Fatalf("CopyPrimaryKey error: %v", err)
	}
	if dst.ID != 42 {
		t.Fatalf("expected dst.ID 42, got %d", dst.ID)
	}

	// 3. ZeroPrimaryKey
	ZeroPrimaryKey(&item)
	if item.ID != 0 {
		t.Fatalf("expected item.ID to be reset to 0, got %d", item.ID)
	}
}
