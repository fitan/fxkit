package authz

import (
	"testing"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestGormAdapter_Persist(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:casbin_persist?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
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

	// Reload from DB
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
