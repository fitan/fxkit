package authz

import (
	"strings"
	"testing"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openSQLite(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func testDBEnforcer(t *testing.T, db *gorm.DB) *Enforcer {
	t.Helper()
	adapter, err := newGormAdapter(db, "casbin_rule")
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.NewModelFromString(defaultRBACModel)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := casbin.NewEnforcer(m, adapter)
	if err != nil {
		t.Fatal(err)
	}
	raw.EnableAutoSave(true)
	if err := raw.LoadPolicy(); err != nil {
		t.Fatal(err)
	}
	if err := ensureRoleTable(db); err != nil {
		t.Fatal(err)
	}
	return &Enforcer{e: raw, db: db, enabled: true}
}

func TestGormAdapter_EmptyTable(t *testing.T) {
	db := openSQLite(t, "file:casbin_empty_table?mode=memory&cache=shared")
	_, err := newGormAdapter(db, "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGormAdapter_Persist(t *testing.T) {
	db := openSQLite(t, "file:casbin_persist?mode=memory&cache=shared")
	adapter, err := newGormAdapter(db, "casbin_rule")
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.NewModelFromString(defaultRBACModel)
	if err != nil {
		t.Fatal(err)
	}
	e, err := casbin.NewEnforcer(m, adapter)
	if err != nil {
		t.Fatal(err)
	}
	e.EnableAutoSave(true)
	if _, err := e.AddPolicy("admin", "/users", "GET"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddGroupingPolicy("alice", "admin"); err != nil {
		t.Fatal(err)
	}

	m2, err := model.NewModelFromString(defaultRBACModel)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := casbin.NewEnforcer(m2, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if err := e2.LoadPolicy(); err != nil {
		t.Fatal(err)
	}
	ok, err := e2.Enforce("alice", "/users", "GET")
	if err != nil || !ok {
		t.Fatalf("reload enforce=%v err=%v", ok, err)
	}
}

func TestEnforcer_ReloadSeesOtherReplica(t *testing.T) {
	db := openSQLite(t, "file:casbin_reload?mode=memory&cache=shared")
	a := testDBEnforcer(t, db)
	b := testDBEnforcer(t, db)
	ctx := t.Context()
	if err := a.AddPolicies([]Policy{{Sub: "admin", Obj: "/orders", Act: "GET"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.AddRoleBindings([]RoleBinding{{User: "carol", Role: "admin"}}); err != nil {
		t.Fatal(err)
	}
	ok, err := b.Enforce(ctx, EnforceInput{Sub: "carol", Obj: "/orders", Act: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("replica B should not see A's policy until reload")
	}
	if err := b.ReloadPolicies(); err != nil {
		t.Fatal(err)
	}
	ok, err = b.Enforce(ctx, EnforceInput{Sub: "carol", Obj: "/orders", Act: "GET"})
	if err != nil || !ok {
		t.Fatalf("after reload enforce=%v err=%v", ok, err)
	}
}

func TestGormAdapter_LongPath(t *testing.T) {
	db := openSQLite(t, "file:casbin_longpath?mode=memory&cache=shared")
	e := testDBEnforcer(t, db)
	path := "/" + strings.Repeat("seg/", 40) + "item" // > 100 chars
	if len(path) < 101 {
		t.Fatalf("path too short: %d", len(path))
	}
	if err := e.AddPolicies([]Policy{{Sub: "admin", Obj: path, Act: "GET"}}); err != nil {
		t.Fatal(err)
	}
	other := testDBEnforcer(t, db)
	if err := other.ReloadPolicies(); err != nil {
		t.Fatal(err)
	}
	ok, err := other.Enforce(t.Context(), EnforceInput{Sub: "admin", Obj: path, Act: "GET"})
	if err != nil || !ok {
		t.Fatalf("long path enforce=%v err=%v len=%d", ok, err, len(path))
	}
}

func TestSetRolePermissions_PersistsForReload(t *testing.T) {
	db := openSQLite(t, "file:casbin_setrole?mode=memory&cache=shared")
	e := testDBEnforcer(t, db)
	ctx := t.Context()
	if err := e.SetRolePermissions(ctx, SetRolePermissionsInput{
		Role: "editor",
		Items: []RolePermission{
			{Path: "/users", Method: "GET"},
			{Path: "/users/{id}", Method: "GET"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetRolePermissions(ctx, SetRolePermissionsInput{
		Role:  "editor",
		Items: []RolePermission{{Path: "/users", Method: "POST"}},
	}); err != nil {
		t.Fatal(err)
	}
	other := testDBEnforcer(t, db)
	if err := other.ReloadPolicies(); err != nil {
		t.Fatal(err)
	}
	perms, err := other.ListRolePermissions(ctx, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if len(perms) != 1 || perms[0].Path != "/users" || perms[0].Method != "POST" {
		t.Fatalf("want replaced POST /users, got %+v", perms)
	}
}

func TestSetSubjectRoles_PersistsForReload(t *testing.T) {
	db := openSQLite(t, "file:casbin_setsubject?mode=memory&cache=shared")
	e := testDBEnforcer(t, db)
	ctx := t.Context()
	if err := e.AddPolicies([]Policy{{Sub: "admin", Obj: "/users", Act: "GET"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetSubjectRoles(ctx, SubjectRolesInput{
		Subject: "dave",
		Roles:   []string{"admin", "ghost"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetSubjectRoles(ctx, SubjectRolesInput{
		Subject: "dave",
		Roles:   []string{"admin"},
	}); err != nil {
		t.Fatal(err)
	}
	other := testDBEnforcer(t, db)
	if err := other.ReloadPolicies(); err != nil {
		t.Fatal(err)
	}
	roles, err := other.ListSubjectRoles(ctx, "dave")
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf("roles=%v", roles)
	}
	ok, err := other.Enforce(ctx, EnforceInput{Sub: "dave", Obj: "/users", Act: "GET"})
	if err != nil || !ok {
		t.Fatalf("enforce=%v err=%v", ok, err)
	}
}

func TestGormAdapter_RemoveFilteredPolicy(t *testing.T) {
	db := openSQLite(t, "file:casbin_remove_filtered?mode=memory&cache=shared")
	adapter, err := newGormAdapter(db, "casbin_rule")
	if err != nil {
		t.Fatal(err)
	}
	m, err := model.NewModelFromString(defaultRBACModel)
	if err != nil {
		t.Fatal(err)
	}
	e, err := casbin.NewEnforcer(m, adapter)
	if err != nil {
		t.Fatal(err)
	}
	e.EnableAutoSave(true)

	if _, err := e.AddPolicy("admin", "/users", "GET"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddPolicy("editor", "/orders", "GET"); err != nil {
		t.Fatal(err)
	}

	// Remove with fieldIndex = 1 (matching obj="/users")
	if _, err := e.RemoveFilteredPolicy(1, "/users"); err != nil {
		t.Fatal(err)
	}

	// admin, /users, GET should be removed
	ok, err := e.Enforce("admin", "/users", "GET")
	if err != nil || ok {
		t.Fatalf("expected admin /users removed, got ok=%v err=%v", ok, err)
	}

	// editor, /orders, GET MUST still be present
	ok, err = e.Enforce("editor", "/orders", "GET")
	if err != nil || !ok {
		t.Fatalf("expected editor /orders preserved, got ok=%v err=%v", ok, err)
	}
}
