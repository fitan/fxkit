package crudx

import (
	"testing"

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

func TestApplyList_sortInjectionRejected(t *testing.T) {
	db := setupUsersDB(t)
	spec := testSpec()
	spec.SortFields["id"] = "users.id"
	params := &ListParams{
		Limit:         10,
		SortBy:        "id; DROP TABLE users",
		SortDirection: "asc",
	}
	tx, err := ApplyList(db.Model(&struct{}{}).Table("users"), spec, params)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := tx.Pluck("users.name", &names).Error; err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(names))
	}
}
