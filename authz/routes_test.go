package authz

import (
	"net/http"
	"testing"
)

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
