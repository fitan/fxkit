package crudx

import (
	"context"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupUsersDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		name TEXT,
		state TEXT,
		cpu_num INTEGER,
		memory INTEGER,
		enable INTEGER,
		description TEXT,
		cluster_id INTEGER
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE clusters (
		id INTEGER PRIMARY KEY,
		name TEXT
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO clusters (id, name) VALUES (1, 'prod-cluster')`).Error; err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		id, cpu, mem int
		name, state  string
		desc         *string
		clusterID    int
	}{
		{1, 8, 4096, "Alice", "Running", nil, 1},
		{2, 2, 1024, "Bob", "Stopped", strPtr("notes"), 1},
		{3, 4, 2048, "prod-user", "Running", strPtr("prod"), 1},
	}
	for _, r := range rows {
		if err := db.Exec(`INSERT INTO users (id, name, state, cpu_num, memory, enable, description, cluster_id)
			VALUES (?, ?, ?, ?, ?, 1, ?, ?)`, r.id, r.name, r.state, r.cpu, r.mem, r.desc, r.clusterID).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func strPtr(s string) *string { return &s }

func TestApplyList_filters(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	params := &ListParams{
		Limit: 10,
		Q:     []string{"state=Running", "cpuNum>=4"},
	}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := tx.Pluck("users.name", &names).Error; err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("want 2 rows, got %v", names)
	}
}

func TestApplyList_likeJoin(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	params := &ListParams{
		Limit: 10,
		Q:     []string{"name~prod", "cluster.name~prod"},
	}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want 1 row, got %d", count)
	}
}

func TestApplyList_null(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	params := &ListParams{
		Limit: 10,
		Q:     []string{"description=null"},
	}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want 1 row with null description, got %d", count)
	}
}

func TestApplyList_inOR(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	params := &ListParams{
		Limit: 10,
		Q:     []string{"state=Running|Stopped"},
	}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("want 3 rows, got %d", count)
	}
}

func TestApplyList_notEqual(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	params := &ListParams{Limit: 10, Q: []string{"state!=Stopped"}}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("want 2 non-Stopped rows, got %d", count)
	}
}

func TestApplyList_notLike(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	params := &ListParams{Limit: 10, Q: []string{"name!~prod"}}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("want 2 rows without prod in name, got %d", count)
	}
}

func TestApplyList_notIn(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	params := &ListParams{Limit: 10, Q: []string{"state.notin:Stopped"}}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("want 2 Running rows, got %d", count)
	}
}

func TestApplyList_compareOps(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	params := &ListParams{Limit: 10, Q: []string{"cpuNum>2", "memory<5000"}}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("want 2 rows, got %d", count)
	}
}

func TestApplyList_pagination(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	spec.SortFields["id"] = "users.id"
	params := &ListParams{
		Limit:         1,
		Start:         1,
		SortBy:        "id",
		SortDirection: "asc",
	}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	if err := tx.Pluck("users.id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("want id=2, got %v", ids)
	}
}

func TestApplyList_joinDedup(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	params := &ListParams{
		Limit: 10,
		Q:     []string{"cluster.name~prod", "cluster.name~cluster"},
	}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("want 3 rows, got %d", count)
	}
}

func TestApplyList_tooManyLikeBlocked(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	spec.Limits.MaxLikeConds = 2
	params := &ListParams{
		Limit: 10,
		Q:     []string{"name~a", "name~b", "name~c"},
	}
	_, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	assertInvalid(t, err, ReasonTooManyLikeConditions)
}

func TestApplyList_cursorNeedsPK(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	spec.SortFields["id"] = "users.id"
	tok, err := EncodeCursor("id", "asc", int64(1), "1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyList(db.Model(&struct{}{}).Table("users"), spec, &ListParams{
		Limit: 10, Cursor: tok, SortBy: "id", SortDirection: "asc",
	})
	assertInvalid(t, err, ReasonCursorNeedsPK)
}

func TestApplyList_cursorOnTypedModel(t *testing.T) {
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
	tok, err := EncodeCursor("id", "asc", int64(1), "1")
	if err != nil {
		t.Fatal(err)
	}
	spec := listTestSpec()
	tx, err := ApplyList(listTestBaseDB(db, ctx), spec, &ListParams{
		Limit: 10, Cursor: tok, SortBy: "id", SortDirection: "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	if err := tx.Pluck("list_test_models.id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != 2 || ids[1] != 3 {
		t.Fatalf("want ids 2,3 got %v", ids)
	}
}

func TestApplyList_sortInjectionRejected(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	spec.SortFields["id"] = "users.id"
	params := &ListParams{
		Limit:         10,
		SortBy:        "id; DROP TABLE users",
		SortDirection: "asc",
	}
	_, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	assertInvalid(t, err, ReasonInvalidSortField)
}

func TestApplyList_unknownSortFieldRejected(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	// 非空但不在白名单的 SortBy 应返回 400，而不是静默忽略（否则 offset 分页会重复/漏行）。
	_, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, &ListParams{
		Limit: 10, SortBy: "nope", SortDirection: "asc",
	})
	assertInvalid(t, err, ReasonInvalidSortField)
}

// TestApplyList_fieldWithoutColumnNotSortable 回归：FieldSpec 存在但没填 Column
// 时无法生成 ORDER BY，必须拒绝而不是拼出空列名。
func TestApplyList_fieldWithoutColumnNotSortable(t *testing.T) {
	db := setupUsersDB(t)
	spec := ListSpec{
		Fields: map[string]FieldSpec{
			"name":  {Column: "users.name", Kind: FieldString},
			"ghost": {Kind: FieldString}, // 只有声明，没有列名
		},
	}
	_, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, &ListParams{
		Limit: 10, SortBy: "ghost", SortDirection: "asc",
	})
	assertInvalid(t, err, ReasonInvalidSortField)
}

func TestApplyList_emptySortUsesNoOrder(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec() // 没有 DefaultSort
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, &ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("want 3 rows, got %d", count)
	}
}

func TestApplyList_nilGuards(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()

	if _, err := ApplyList(nil, spec, &ListParams{Limit: 10}); !fxerrors.Is(err, fxerrors.KindInternal) {
		t.Fatalf("nil db: want internal error, got %v", err)
	}
	if _, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, nil); !fxerrors.Is(err, fxerrors.KindInternal) {
		t.Fatalf("nil params: want internal error, got %v", err)
	}
}

func TestApplyList_inNull(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	cases := []struct {
		q    string
		want int64
	}{
		// description：id1 为 NULL，id2 为 "notes"，id3 为 "prod"
		{"description.in:null", 1},
		{"description.in:notes,null", 2},
		{"description.notin:null", 2},
		{"description.notin:notes,null", 1},
		{"description.in:notes,prod", 2},
	}
	for _, tc := range cases {
		tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, &ListParams{Limit: 10, Q: []string{tc.q}})
		if err != nil {
			t.Fatalf("%s: %v", tc.q, err)
		}
		var count int64
		if err := tx.Count(&count).Error; err != nil {
			t.Fatalf("%s: %v", tc.q, err)
		}
		if count != tc.want {
			t.Fatalf("%s: want %d rows, got %d", tc.q, tc.want, count)
		}
	}
}

func TestApplyList_nullInValuesWithEqualOR(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, &ListParams{
		Limit: 10, Q: []string{"description=notes|null"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("want 2 rows (notes + NULL), got %d", count)
	}
}

func TestApplyList_sortWithJoinAndDistinct(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	spec.SortFields["name"] = "users.name"
	// JOIN 会触发 SELECT DISTINCT；NULL 序首列必须在该场景下依然能执行。
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, &ListParams{
		Limit:         10,
		Q:             []string{"name~prod", "cluster.name~prod"},
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := tx.Pluck("users.name", &names).Error; err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "prod-user" {
		t.Fatalf("want [prod-user], got %v", names)
	}
}

func TestApplyList_likeEscapesBang(t *testing.T) {
	db := setupUsersDB(t)
	if err := db.Exec(`INSERT INTO users (id, name, state, cpu_num, memory, enable, description, cluster_id)
		VALUES (4, 'hello!world', 'Running', 1, 1, 1, NULL, 1), (5, 'helloXworld', 'Running', 1, 1, 1, NULL, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	spec := testSpec()
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, &ListParams{
		Limit: 10, Q: []string{"name~hello!"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := tx.Pluck("users.name", &names).Error; err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "hello!world" {
		t.Fatalf("want [hello!world], got %v", names)
	}
}

func TestApplyList_hasManyDedup(t *testing.T) {
	db := setupUsersDB(t)
	if err := db.Exec(`CREATE TABLE tags (id INTEGER PRIMARY KEY, user_id INTEGER, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tags (user_id, name) VALUES (1,'red'), (1,'blue'), (1,'green')`).Error; err != nil {
		t.Fatal(err)
	}
	spec := testSpec()
	spec.Relations["tag"] = &RelationSpec{
		Table: "tags", Alias: "tag", On: "tag.user_id = users.id",
		Fields: map[string]FieldSpec{
			"name": {Column: "tag.name", Kind: FieldString},
		},
	}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, &ListParams{
		Limit: 10, Q: []string{"tag.name!=zzz"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := tx.Pluck("users.name", &names).Error; err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "Alice" {
		t.Fatalf("want distinct Alice, got %v", names)
	}
}

func TestApplyList_cursorRejectsRelationSort(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	tok, err := EncodeCursor("cluster.name", "asc", "prod", "1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyList(db.Model(&struct{}{}).Table("users"), spec, &ListParams{
		Limit: 10, Cursor: tok, SortBy: "cluster.name", SortDirection: "asc",
	})
	assertInvalid(t, err, ReasonCursorRelationSort)
}

// TestApplyList_distinctOnlyForToManyJoins 基数已知的多对一关联不需要 DISTINCT。
func TestApplyList_distinctOnlyForToManyJoins(t *testing.T) {
	db := setupUsersDB(t)

	run := func(card RelationCardinality) bool {
		spec := testSpec()
		spec.Relations["cluster"].Cardinality = card
		tx := db.Session(&gorm.Session{DryRun: true}).Model(&struct{}{}).Table("users")
		out, jc, err := applyList(tx, spec, &ListParams{Limit: 10, Q: []string{"cluster.name=prod-cluster"}}, listApplyOpts{})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if jc != 1 {
			t.Fatalf("joinCount=%d, want 1", jc)
		}
		return out.Statement.Distinct
	}

	if !run(RelationUnknown) {
		t.Fatal("unknown cardinality must keep DISTINCT (旧行为不能变)")
	}
	if run(RelationToOne) {
		t.Fatal("to-one join should skip DISTINCT")
	}
	if !run(RelationToMany) {
		t.Fatal("to-many join must keep DISTINCT")
	}
}
