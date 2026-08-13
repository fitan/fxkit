package crudx

import (
	"context"
	"testing"
	"time"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type listTestModel struct {
	ID        int64  `gorm:"primaryKey"`
	Name      string `gorm:"size:64;not null"`
	Email     string `gorm:"size:128;uniqueIndex;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type listTestRow struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func listTestSpec() ListSpec {
	return ListSpec{
		Fields: map[string]FieldSpec{
			"name":  {Column: "list_test_models.name", Kind: FieldString, Indexed: true},
			"email": {Column: "list_test_models.email", Kind: FieldString, Indexed: true},
		},
		SortFields: map[string]string{
			"id":   "list_test_models.id",
			"name": "list_test_models.name",
		},
		DefaultSort: "id asc",
		Limits:      Limits{LimitDefault: 20, LimitMax: 100},
	}
}

func newListTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&listTestModel{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveModel[listTestModel](db); err != nil {
		t.Fatal(err)
	}
	return db
}

func listTestBaseDB(db *gorm.DB, ctx context.Context) *gorm.DB {
	return db.WithContext(ctx).Model(&listTestModel{}).Table("list_test_models")
}

func TestList_FilterAndCount(t *testing.T) {
	db := newListTestDB(t)
	ctx := context.Background()
	for _, m := range []listTestModel{
		{Name: "Alice", Email: "alice@example.com"},
		{Name: "Bob", Email: "bob@example.com"},
		{Name: "prod-user", Email: "prod@example.com"},
	} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}

	out, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   listTestSpec(),
		Params: ListParams{Limit: 10, ReplyWithCount: true, Q: []string{"name~prod"}},
		ToRow:  func(m listTestModel) listTestRow { return listTestRow{ID: m.ID, Name: m.Name} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total == nil || *out.Total != 1 {
		t.Fatalf("total=%v", out.Total)
	}
	if len(out.Items) != 1 || out.Items[0].Name != "prod-user" {
		t.Fatalf("items=%+v", out.Items)
	}
}

func TestList_CountWithSmallLimit(t *testing.T) {
	db := newListTestDB(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		m := listTestModel{Name: "same", Email: "u" + string(rune('a'+i)) + "@example.com"}
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	out, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   listTestSpec(),
		Params: ListParams{Limit: 2, ReplyWithCount: true, Q: []string{"name=same"}},
		ToRow:  func(m listTestModel) listTestRow { return listTestRow{ID: m.ID, Name: m.Name} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total == nil || *out.Total != 5 {
		t.Fatalf("total=%v want 5", out.Total)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items=%d want 2", len(out.Items))
	}
}

func TestList_LikeEscapesWildcards(t *testing.T) {
	db := newListTestDB(t)
	ctx := context.Background()
	for _, m := range []listTestModel{
		{Name: "100%_done", Email: "a@example.com"},
		{Name: "100Xdone", Email: "b@example.com"},
	} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	out, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   listTestSpec(),
		Params: ListParams{Limit: 10, Q: []string{"name~100%_"}},
		ToRow:  func(m listTestModel) listTestRow { return listTestRow{ID: m.ID, Name: m.Name} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].Name != "100%_done" {
		t.Fatalf("items=%+v", out.Items)
	}
}

func TestList_CursorAndOR(t *testing.T) {
	db := newListTestDB(t)
	ctx := context.Background()
	for _, m := range []listTestModel{
		{Name: "A", Email: "a@example.com"},
		{Name: "B", Email: "b@example.com"},
		{Name: "C", Email: "c@example.com"},
	} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	spec := listTestSpec()
	toRow := func(m listTestModel) listTestRow { return listTestRow{ID: m.ID, Name: m.Name} }

	page1, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   spec,
		Params: ListParams{Limit: 2, UseCursor: true, SortBy: "id", SortDirection: "asc"},
		ToRow:  toRow,
	})
	if err != nil || len(page1.Items) != 2 || page1.NextCursor == nil {
		t.Fatalf("page1: %+v err=%v", page1, err)
	}
	page2, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   spec,
		Params: ListParams{Limit: 2, Cursor: *page1.NextCursor, SortBy: "id", SortDirection: "asc"},
		ToRow:  toRow,
	})
	if err != nil || len(page2.Items) != 1 || page2.Items[0].Name != "C" {
		t.Fatalf("page2: %+v err=%v", page2, err)
	}

	orList, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   spec,
		Params: ListParams{Limit: 10, Q: []string{"or(name=A,name=C)"}},
		ToRow:  toRow,
	})
	if err != nil || len(orList.Items) != 2 {
		t.Fatalf("OR list: %+v err=%v", orList, err)
	}
}

func TestGetByID(t *testing.T) {
	db := newListTestDB(t)
	ctx := context.Background()
	if err := db.Create(&listTestModel{Name: "Alice", Email: "alice@example.com"}).Error; err != nil {
		t.Fatal(err)
	}
	detail, err := GetByID(ctx, db, "1", func(m listTestModel) listTestRow {
		return listTestRow{ID: m.ID, Name: m.Name}
	})
	if err != nil || detail.Name != "Alice" {
		t.Fatalf("GetByID: %+v err=%v", detail, err)
	}
	_, err = GetByID(ctx, db, "404", func(m listTestModel) listTestRow {
		return listTestRow{ID: m.ID, Name: m.Name}
	})
	if !fxerrors.Is(err, fxerrors.KindNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestFormatPrimaryKey(t *testing.T) {
	newListTestDB(t) // resolves + caches ModelMeta for listTestModel
	m := listTestModel{ID: 42}
	id, err := FormatPrimaryKey(&m)
	if err != nil || id != "42" {
		t.Fatalf("FormatPrimaryKey = %q err=%v", id, err)
	}
}

func TestParseQ_IsNullAndOR(t *testing.T) {
	spec := testSpec()
	conds, err := ParseQConditions(spec, []string{"description.isnull"})
	if err != nil || len(conds) != 1 || conds[0].Op != OpIsNull {
		t.Fatalf("isnull: %+v err=%v", conds, err)
	}
	groups, err := ParseQGroups(spec, []string{"or(state=Running,state=Stopped)", "name~a"})
	if err != nil || len(groups) != 2 || !groups[0].Or || len(groups[0].Conds) != 2 {
		t.Fatalf("groups: %+v err=%v", groups, err)
	}
}
