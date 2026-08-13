package authz

import (
	"fmt"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestHTTPRouteFromOperation(t *testing.T) {
	op := huma.Operation{
		OperationID: "listUsers", Method: "GET", Path: "/users",
		Summary: "列出用户", Description: "分页列表", Tags: []string{"Users"},
	}
	r := HTTPRouteFromOperation(op)
	if r.OperationID != "listUsers" || r.Summary != "列出用户" || r.Path != "/users" {
		t.Fatalf("%+v", r)
	}
	if len(r.Tags) != 1 || r.Tags[0] != "Users" {
		t.Fatalf("tags=%v", r.Tags)
	}
}

func TestSyncRouteCatalog(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:authz_catalog?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &Enforcer{db: db, enabled: true}
	if err := ensurePermissionCatalog(db); err != nil {
		t.Fatal(err)
	}
	routes := HTTPRoutesFromOperations(huma.Operation{
		OperationID: "listUsers", Method: "GET", Path: "/users", Summary: "列出用户", Tags: []string{"Users"},
	})
	if err := e.SyncRouteCatalog(routes); err != nil {
		t.Fatal(err)
	}
	// upsert again with updated summary
	routes[0].Summary = "列出全部用户"
	if err := e.SyncRouteCatalog(routes); err != nil {
		t.Fatal(err)
	}
	rows, err := e.ListRouteCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("len=%d", len(rows))
	}
	if rows[0].Summary != "列出全部用户" || rows[0].OperationID != "listUsers" {
		t.Fatalf("%+v", rows[0])
	}
	if rows[0].Pattern != "/users" || rows[0].Tags != "Users" {
		t.Fatalf("%+v", rows[0])
	}

	// seed more rows for paging
	for i := 0; i < 5; i++ {
		if _, err := e.CreateAPIPermission(t.Context(), CreateAPIPermissionInput{
			Method: "GET", Path: fmt.Sprintf("/paged/%d", i), Summary: "page item",
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := e.ListRouteCatalogPage(t.Context(), ListRouteCatalogInput{
		Start: 0, Limit: 2, ReplyWithCount: true, Q: "paged",
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total == nil || *page.Total != 5 || len(page.Items) != 2 {
		t.Fatalf("page=%+v total=%v", page.Items, page.Total)
	}
	page2, err := e.ListRouteCatalogPage(t.Context(), ListRouteCatalogInput{
		Start: 2, Limit: 2, ReplyWithCount: true, Q: "paged",
	})
	if err != nil || len(page2.Items) != 2 {
		t.Fatalf("page2=%+v err=%v", page2, err)
	}

	created, err := e.CreateAPIPermission(t.Context(), CreateAPIPermissionInput{
		Method: "POST", Path: "/users", Summary: "创建用户", Tags: []string{"Users", "Users"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.Pattern != "/users" || created.Tags != "Users" {
		t.Fatalf("%+v", created)
	}
	if _, err := e.CreateAPIPermission(t.Context(), CreateAPIPermissionInput{
		Method: "POST", Path: "/users",
	}); err == nil {
		t.Fatal("expected conflict")
	}

	updated, err := e.UpdateAPIPermission(t.Context(), UpdateAPIPermissionInput{
		ID: created.ID, Method: "POST", Path: "/users/{id}", Summary: "更新用户",
		OperationID: "updateUser", Tags: []string{"Users"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Path != "/users/{id}" || updated.Pattern != "/users/:id" || updated.Summary != "更新用户" {
		t.Fatalf("%+v", updated)
	}

	got, err := e.GetAPIPermission(t.Context(), created.ID)
	if err != nil || got.OperationID != "updateUser" {
		t.Fatalf("%+v err=%v", got, err)
	}
	if err := e.DeleteAPIPermission(t.Context(), DeleteAPIPermissionInput{ID: created.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GetAPIPermission(t.Context(), created.ID); err == nil {
		t.Fatal("expected not found after delete")
	}
}
