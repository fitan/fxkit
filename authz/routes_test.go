package authz

import (
	"net/http"
	"testing"
)

func TestFindRouteTrailingSlash(t *testing.T) {
	routes := []HTTPRoute{{Method: http.MethodGet, Path: "/users", Permission: "users:list"}}
	got, ok := findRoute(routes, http.MethodGet, "/users/")
	if !ok || got.Permission != "users:list" {
		t.Fatalf("got=%+v ok=%v", got, ok)
	}
	if got := normalizeRequestPath("/users/"); got != "/users" {
		t.Fatalf("normalize=%q", got)
	}
	if got := normalizeRequestPath("/"); got != "/" {
		t.Fatalf("root=%q", got)
	}
}

func TestFindRoutePrefersSpecific(t *testing.T) {
	routes := []HTTPRoute{
		{Method: "", Path: "/users/{id}", Permission: "users:get"},
		{Method: http.MethodGet, Path: "/users/{id}", Permission: "users:read"},
		{Method: http.MethodGet, Path: "/users/me", Permission: "users:me"},
	}
	got, ok := findRoute(routes, http.MethodGet, "/users/me")
	if !ok || got.Permission != "users:me" {
		t.Fatalf("got=%+v ok=%v", got, ok)
	}
	got, ok = findRoute(routes, http.MethodGet, "/users/42")
	if !ok || got.Permission != "users:read" {
		t.Fatalf("got=%+v ok=%v", got, ok)
	}
	got, ok = findRoute(routes, http.MethodDelete, "/users/42")
	if !ok || got.Permission != "users:get" {
		t.Fatalf("got=%+v ok=%v", got, ok)
	}
}

func TestSubjectHasWildcard(t *testing.T) {
	s := Subject{Permissions: []string{"orders:*"}}
	if !s.Has("orders:read") {
		t.Fatal("orders:* should match orders:read")
	}
	if s.Has("orders:a:b") {
		t.Fatal("orders:* should not match multi-segment orders:a:b")
	}
	if s.Has("users:read") {
		t.Fatal("orders:* should not match users:read")
	}
}

func TestSkipAuthzPath(t *testing.T) {
	if !skipAuthzPath("/huma/openapi.json") || !skipAuthzPath("/schemas/Foo") || !skipAuthzPath("/openapi.json") {
		t.Fatal("builtin spec paths should skip authz")
	}
	if skipAuthzPath("/users") {
		t.Fatal("/users should not skip authz")
	}
}

func TestAdminHTTPRoutesRequirePermission(t *testing.T) {
	routes := adminHTTPRoutes()
	if len(routes) == 0 {
		t.Fatal("expected admin routes")
	}
	for _, r := range routes {
		if r.Permission != AdminPermission {
			t.Fatalf("route %s %s permission=%q want %q", r.Method, r.Path, r.Permission, AdminPermission)
		}
	}
}
