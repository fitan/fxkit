package crudx

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 这组测试用来「把 SQL 方言风险关掉」：
//   - 默认总是跑 SQLite（本地/普通 CI 即可验证逻辑）；
//   - 设置 FXKIT_TEST_POSTGRES_DSN / FXKIT_TEST_MYSQL_DSN 后，同一批断言在真实
//     PostgreSQL / MySQL 上再跑一遍（CI 的 dialect job 用 service containers 注入）。
//
// 重点覆盖：游标翻页（asc/desc、只带 cursor）、可空排序列、LIKE 字面量 %、
// JOIN（to-one 不加 DISTINCT / to-many 加 DISTINCT）、非法排序。

type dialectOrg struct {
	ID   uint64 `gorm:"primaryKey"`
	Name string `gorm:"size:64;not null"`
}

func (dialectOrg) TableName() string { return "fxkit_dialect_orgs" }

type dialectItem struct {
	ID        uint64 `gorm:"primaryKey"`
	Title     string `gorm:"size:64;not null"`
	Status    string `gorm:"size:16;not null"`
	OrgID     uint64 `gorm:"index"`
	Note      *string
	CreatedAt time.Time
}

func (dialectItem) TableName() string { return "fxkit_dialect_items" }

func dialectDBs(t *testing.T) map[string]*gorm.DB {
	t.Helper()

	out := map[string]*gorm.DB{}
	sqliteDB, err := gorm.Open(sqlite.Open("file:dialect?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	out["sqlite"] = sqliteDB

	if dsn := os.Getenv("FXKIT_TEST_POSTGRES_DSN"); dsn != "" {
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err, "open postgres")
		out["postgres"] = db
	}
	if dsn := os.Getenv("FXKIT_TEST_MYSQL_DSN"); dsn != "" {
		db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err, "open mysql")
		out["mysql"] = db
	}
	return out
}

func seedDialect(t *testing.T, db *gorm.DB) {
	t.Helper()

	require.NoError(t, db.Migrator().DropTable(&dialectItem{}, &dialectOrg{}))
	require.NoError(t, db.AutoMigrate(&dialectOrg{}, &dialectItem{}))
	require.NoError(t, db.Create(&[]dialectOrg{
		{ID: 1, Name: "acme"},
		{ID: 2, Name: "other"},
	}).Error)

	note := "n1"
	items := []dialectItem{
		{ID: 1, Title: "alpha", Status: "on", OrgID: 1, Note: &note},
		{ID: 2, Title: "beta", Status: "off", OrgID: 1},
		{ID: 3, Title: "gamma 50%", Status: "on", OrgID: 2},
		{ID: 4, Title: "delta", Status: "on", OrgID: 1},
		{ID: 5, Title: "epsilon", Status: "off", OrgID: 2},
	}
	require.NoError(t, db.Create(&items).Error)
}

// dialectSpec 构造白名单。toOne=false 时关联基数未知（必须 DISTINCT）。
func dialectSpec(toOne bool) ListSpec {
	card := RelationUnknown
	if toOne {
		card = RelationToOne
	}
	return ListSpec{
		Fields: map[string]FieldSpec{
			"id":     {Column: "fxkit_dialect_items.id", Kind: FieldNumber},
			"title":  {Column: "fxkit_dialect_items.title", Kind: FieldString, Likeable: true},
			"status": {Column: "fxkit_dialect_items.status", Kind: FieldString},
			"note":   {Column: "fxkit_dialect_items.note", Kind: FieldString},
		},
		Relations: map[string]*RelationSpec{
			"org": {
				Table:       "fxkit_dialect_orgs",
				Alias:       "org",
				On:          "org.id = fxkit_dialect_items.org_id",
				Fields:      map[string]FieldSpec{"name": {Column: "org.name", Kind: FieldString}},
				Cardinality: card,
			},
		},
		SortFields: map[string]string{
			"id":       "fxkit_dialect_items.id",
			"note":     "fxkit_dialect_items.note",
			"org_name": "org.name",
		},
		DefaultSort: "id",
		Limits:      Limits{LimitDefault: 20, LimitMax: 100},
	}
}

func dialectList(t *testing.T, db *gorm.DB, spec ListSpec, params ListParams) ListResult[dialectItem] {
	t.Helper()
	res, err := List(context.Background(), ListInput[dialectItem, dialectItem]{
		DB:     db.Model(&dialectItem{}),
		Spec:   spec,
		Params: params,
		ToRow:  func(m dialectItem) dialectItem { return m },
	})
	require.NoError(t, err)
	return res
}

func idsOf(items []dialectItem) []uint64 {
	out := make([]uint64, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestDialectCursorPaginationDesc(t *testing.T) {
	for name, db := range dialectDBs(t) {
		t.Run(name, func(t *testing.T) {
			seedDialect(t, db)
			spec := dialectSpec(true)

			// page1：显式 desc
			page1 := dialectList(t, db, spec, ListParams{Limit: 2, SortBy: "id", SortDirection: "desc", UseCursor: true})
			require.Equal(t, []uint64{5, 4}, idsOf(page1.Items))
			require.NotNil(t, page1.NextCursor)

			// page2：只带 cursor（回归：以前会被 Huma 的 default:"asc" 打成 400）
			page2 := dialectList(t, db, spec, ListParams{Limit: 2, Cursor: *page1.NextCursor})
			require.Equal(t, []uint64{3, 2}, idsOf(page2.Items))
			require.NotNil(t, page2.NextCursor)

			page3 := dialectList(t, db, spec, ListParams{Limit: 2, Cursor: *page2.NextCursor})
			require.Equal(t, []uint64{1}, idsOf(page3.Items))
			require.Nil(t, page3.NextCursor)
		})
	}
}

func TestDialectNullableSortDoesNotDropRows(t *testing.T) {
	for name, db := range dialectDBs(t) {
		t.Run(name, func(t *testing.T) {
			seedDialect(t, db)
			spec := dialectSpec(true)

			var got []uint64
			params := ListParams{Limit: 2, SortBy: "note", SortDirection: "asc", UseCursor: true}
			for i := 0; i < 10; i++ {
				page := dialectList(t, db, spec, params)
				got = append(got, idsOf(page.Items)...)
				if page.NextCursor == nil {
					break
				}
				params = ListParams{Limit: 2, Cursor: *page.NextCursor}
			}
			// 1 条有值 + 4 条 NULL；NULL 统一排最后，共 5 条不能丢。
			require.Len(t, got, 5, "nullable sort lost rows: %v", got)
			require.Equal(t, uint64(1), got[0])
		})
	}
}

func TestDialectJoinToOneNoDistinct(t *testing.T) {
	for name, db := range dialectDBs(t) {
		t.Run(name, func(t *testing.T) {
			seedDialect(t, db)
			spec := dialectSpec(true) // 多对一：无需 DISTINCT，可以按关联列排序

			res := dialectList(t, db, spec, ListParams{
				Limit: 20, Q: []string{"org.name=acme"}, SortBy: "org.name", SortDirection: "asc",
			})
			require.Equal(t, []uint64{1, 2, 4}, idsOf(res.Items))
		})
	}
}

func TestDialectJoinDistinctSortByRoot(t *testing.T) {
	for name, db := range dialectDBs(t) {
		t.Run(name, func(t *testing.T) {
			seedDialect(t, db)
			spec := dialectSpec(false) // 基数未知 → DISTINCT

			res := dialectList(t, db, spec, ListParams{
				Limit: 20, Q: []string{"org.name=acme"}, SortBy: "id",
			})
			require.Equal(t, []uint64{1, 2, 4}, idsOf(res.Items))
		})
	}
}

func TestDialectJoinDistinctRejectsRelationSort(t *testing.T) {
	for name, db := range dialectDBs(t) {
		t.Run(name, func(t *testing.T) {
			seedDialect(t, db)
			spec := dialectSpec(false)

			_, err := List(context.Background(), ListInput[dialectItem, dialectItem]{
				DB:     db.Model(&dialectItem{}),
				Spec:   spec,
				Params: ListParams{Limit: 20, Q: []string{"org.name=acme"}, SortBy: "org.name"},
				ToRow:  func(m dialectItem) dialectItem { return m },
			})
			require.Error(t, err)
			var fe *fxerrors.Error
			require.True(t, errors.As(err, &fe), "want fxerrors.Error, got %T", err)
			require.Equal(t, ReasonDistinctRelationSort, fe.Details["reason"])
		})
	}
}

func TestDialectLikeLiteralPercent(t *testing.T) {
	for name, db := range dialectDBs(t) {
		t.Run(name, func(t *testing.T) {
			seedDialect(t, db)
			spec := dialectSpec(true)

			// 字面量 % 必须按原文匹配：只有 title="gamma 50%" 命中。
			// 这条同时验证 MySQL 的 `LIKE ... ESCAPE '!'` 不会语法报错。
			res := dialectList(t, db, spec, ListParams{Limit: 20, Q: []string{"title~50%"}})
			require.Equal(t, []uint64{3}, idsOf(res.Items))
		})
	}
}

func TestDialectInvalidSortRejected(t *testing.T) {
	for name, db := range dialectDBs(t) {
		t.Run(name, func(t *testing.T) {
			seedDialect(t, db)
			spec := dialectSpec(true)

			_, err := List(context.Background(), ListInput[dialectItem, dialectItem]{
				DB:     db.Model(&dialectItem{}),
				Spec:   spec,
				Params: ListParams{Limit: 20, SortBy: "nope"},
				ToRow:  func(m dialectItem) dialectItem { return m },
			})
			require.Error(t, err)
			var fe *fxerrors.Error
			require.True(t, errors.As(err, &fe))
			require.Equal(t, ReasonInvalidSortField, fe.Details["reason"])
		})
	}
}
