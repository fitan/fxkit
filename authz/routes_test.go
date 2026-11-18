package authz

import (
	"testing"
)

func TestNormalizeRequestPath(t *testing.T) {
	if got := normalizeRequestPath("/users/"); got != "/users" {
		t.Fatalf("normalize=%q", got)
	}
	if got := normalizeRequestPath("/"); got != "/" {
		t.Fatalf("root=%q", got)
	}
}

func TestSkipAuthzPath(t *testing.T) {
	if !skipAuthzPath("/huma/openapi.json") || !skipAuthzPath("/schemas/Foo") || !skipAuthzPath("/openapi.json") {
		t.Fatal("builtin spec paths should skip authz")
	}
	if !skipAuthzPath("/openapi-3.0.json") || !skipAuthzPath("/openapi.yaml") || !skipAuthzPath("/huma/openapi-3.0.yaml") || !skipAuthzPath("/healthz") || !skipAuthzPath("/docs") {
		t.Fatal("spec filenames, probes, and docs should skip authz")
	}
	for _, p := range []string{"/users", "/docs-admin", "/docs-evil", "/version-history", "/healthz-internal", "/schemas-private", "/openapix", "/readyz.x", "/openapi/v1/pets", "/openapi-apps", "/openapi.admin"} {
		if skipAuthzPath(p) {
			t.Fatalf("%s should not skip authz", p)
		}
	}
}

func TestAdminHTTPRoutes(t *testing.T) {
	routes := adminHTTPRoutes()
	if len(routes) == 0 {
		t.Fatal("expected admin routes")
	}
	for _, r := range routes {
		if r.Method == "" || r.Path == "" {
			t.Fatalf("route missing method or path: %+v", r)
		}
	}
}

func TestSubjectHas(t *testing.T) {
	s := Subject{Permissions: []string{"users:list", "orders:*"}}
	if !s.Has("users:list") || s.Has("users:create") {
		t.Fatalf("exact match failed: %+v", s.Permissions)
	}
	if !s.Has("orders:read") || !s.Has("orders:write") {
		t.Fatalf("prefix wildcard failed")
	}
	admin := Subject{Permissions: []string{"*"}}
	if !admin.Has("anything") {
		t.Fatal("star wildcard failed")
	}
}

func TestHumaPathToCasbin(t *testing.T) {
	if got := HumaPathToCasbin("/users/{id}"); got != "/users/:id" {
		t.Fatalf("got %q", got)
	}
	if got := HumaPathToCasbin("/users"); got != "/users" {
		t.Fatalf("got %q", got)
	}
}

func TestConfigRejectsRemovedDenyUnregistered(t *testing.T) {
	cfg := Config{DenyUnregistered: true, OrgClaim: "organization_id"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("deny_unregistered: true must fail startup")
	}
}
