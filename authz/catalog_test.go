package authz

import (
	"context"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPRouteFromOperation(t *testing.T) {
	op := huma.Operation{
		OperationID: "listUsers",
		Method:      "GET",
		Path:        "/users",
		Summary:     "列出用户",
		Description: "分页列出",
		Tags:        []string{"Users"},
	}
	r := HTTPRouteFromOperation(op)
	if r.Method != "GET" || r.Path != "/users" || r.OperationID != "listUsers" {
		t.Fatalf("unexpected route: %+v", r)
	}
	if len(r.Tags) != 1 || r.Tags[0] != "Users" {
		t.Fatalf("tags=%v", r.Tags)
	}
}

// TestListRouteCatalogPage_DerivedFromRoutes 覆盖派生视图：
// 目录不再落库，而是直接来自路由表（+ Casbin 策略）。
func TestListRouteCatalogPage_DerivedFromRoutes(t *testing.T) {
	e := &Enforcer{enabled: false} // Casbin 关闭：只有路由，没有 grants
	routes := HTTPRoutesFromOperations(
		huma.Operation{OperationID: "listUsers", Method: "GET", Path: "/users", Summary: "列出用户", Tags: []string{"Users"}},
		huma.Operation{OperationID: "createUser", Method: "POST", Path: "/users"},
	)

	page, err := e.ListRouteCatalogPage(context.Background(), routes, ListRouteCatalogInput{Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	// 按 path, method 排序
	assert.Equal(t, "/users", page.Items[0].Path)
	assert.Equal(t, "GET", page.Items[0].Method)
	assert.Equal(t, "createUser", page.Items[1].OperationID)
}

func TestListRouteCatalogPage_FilterAndPaginate(t *testing.T) {
	e := &Enforcer{}
	routes := HTTPRoutesFromOperations(
		huma.Operation{OperationID: "listUsers", Method: "GET", Path: "/users", Summary: "列出用户"},
		huma.Operation{OperationID: "listOrders", Method: "GET", Path: "/orders", Summary: "列出订单"},
	)

	filtered, err := e.ListRouteCatalogPage(context.Background(), routes, ListRouteCatalogInput{Q: "订单"})
	require.NoError(t, err)
	require.Len(t, filtered.Items, 1)
	assert.Equal(t, "/orders", filtered.Items[0].Path)

	page, err := e.ListRouteCatalogPage(context.Background(), routes, ListRouteCatalogInput{
		Start: 1, Limit: 1, ReplyWithCount: true,
	})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.NotNil(t, page.Total)
	assert.Equal(t, int64(2), *page.Total)
	// 按 path 字典序：/orders < /users，故 start=1 取第二项 /users。
	assert.Equal(t, "/users", page.Items[0].Path)
}

// TestAdminPermissionList_IsReadOnly 确认权限目录只剩只读列表：
// 旧实现还有 create/get/update/delete 四个写接口（维护独立副本的表）。
func TestAdminPermissionList_IsReadOnly(t *testing.T) {
	db, api := humatest.New(t)
	_ = db
	routes := func() []HTTPRoute {
		return HTTPRoutesFromOperations(huma.Operation{
			OperationID: "listUsers", Method: "GET", Path: "/users", Summary: "列出用户",
		})
	}
	registerPermissionCatalog(api, &Enforcer{}, routes)

	resp := api.Get("/authz/permissions")
	assert.Equal(t, 200, resp.Code)

	// 写接口应已不存在。
	assert.Equal(t, 405, api.Post("/authz/permissions", "{}").Code)
	assert.Equal(t, 404, api.Get("/authz/permissions/1").Code)
	assert.Equal(t, 404, api.Put("/authz/permissions/1", "{}").Code)
	assert.Equal(t, 404, api.Delete("/authz/permissions/1").Code)
}
