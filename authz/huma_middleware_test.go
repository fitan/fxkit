package authz_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/fitan/fxkit/authz"
)

type listOutput struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

type mockValidator struct {
	subjects map[string]authz.Subject
}

func (m mockValidator) Validate(_ context.Context, raw string) (authz.Subject, error) {
	if s, ok := m.subjects[raw]; ok {
		return s, nil
	}
	return authz.Subject{}, errors.New("invalid token")
}

func installMiddleware(t *testing.T, cfg authz.HumaMiddlewareConfig) humatest.TestAPI {
	t.Helper()
	_, api := humatest.New(t)
	reg := authz.NewHumaMiddleware(cfg)
	if m := reg.Middleware(api); m != nil {
		api.UseMiddleware(m)
	}
	huma.Register(api, huma.Operation{OperationID: "list-users", Method: http.MethodGet, Path: "/users"},
		func(ctx context.Context, _ *struct{}) (*listOutput, error) {
			out := &listOutput{}
			out.Body.OK = true
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "create-user", Method: http.MethodPost, Path: "/users"},
		func(ctx context.Context, _ *struct{}) (*listOutput, error) {
			out := &listOutput{}
			out.Body.OK = true
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-user", Method: http.MethodGet, Path: "/users/{id}"},
		func(ctx context.Context, _ *struct {
			ID string `path:"id"`
		}) (*listOutput, error) {
			out := &listOutput{}
			out.Body.OK = true
			return out, nil
		})
	return api
}

func testRoutes() []authz.HTTPRoute {
	return []authz.HTTPRoute{
		{Method: "GET", Path: "/users", Permission: "users:list"},
		{Method: "POST", Path: "/users", Permission: "users:create"},
		{Method: "GET", Path: "/users/{id}"},
	}
}

func TestHumaMiddleware_AllowsRegisteredRoute(t *testing.T) {
	api := installMiddleware(t, authz.HumaMiddlewareConfig{
		Enabled:       true,
		DevHeaderUser: true,
		Routes:        testRoutes(),
		SubjectFunc: func(_ context.Context, h http.Header) authz.Subject {
			id := h.Get("X-User")
			if id == "bob" {
				return authz.Subject{ID: id, Permissions: []string{"users:list"}}
			}
			return authz.Subject{}
		},
	})
	resp := api.Get("/users", "X-User: bob")
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestHumaMiddleware_DeniesWithoutPermission(t *testing.T) {
	api := installMiddleware(t, authz.HumaMiddlewareConfig{
		Enabled:       true,
		DevHeaderUser: true,
		Routes:        testRoutes(),
		SubjectFunc: func(_ context.Context, h http.Header) authz.Subject {
			return authz.Subject{ID: h.Get("X-User"), Permissions: []string{"users:list"}}
		},
	})
	resp := api.Post("/users", "X-User: bob", map[string]any{})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestHumaMiddleware_DeniesAnonymous(t *testing.T) {
	api := installMiddleware(t, authz.HumaMiddlewareConfig{
		Enabled:       true,
		DevHeaderUser: true,
		Routes:        testRoutes(),
	})
	resp := api.Get("/users")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestHumaMiddleware_AdminWildcard(t *testing.T) {
	api := installMiddleware(t, authz.HumaMiddlewareConfig{
		Enabled:       true,
		DevHeaderUser: true,
		Routes:        testRoutes(),
		SubjectFunc: func(_ context.Context, h http.Header) authz.Subject {
			return authz.Subject{ID: h.Get("X-User"), Permissions: []string{"*"}}
		},
	})
	resp := api.Post("/users", "X-User: alice", map[string]any{})
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestHumaMiddleware_NoopWhenDisabled(t *testing.T) {
	api := installMiddleware(t, authz.HumaMiddlewareConfig{
		Enabled: false,
		Routes:  testRoutes(),
	})
	resp := api.Get("/users")
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestHumaMiddleware_BearerToken(t *testing.T) {
	api := installMiddleware(t, authz.HumaMiddlewareConfig{
		Enabled: true,
		Validator: mockValidator{subjects: map[string]authz.Subject{
			"tok-alice": {ID: "alice", Permissions: []string{"users:list"}},
		}},
		Routes: testRoutes(),
	})
	resp := api.Get("/users", "Authorization: Bearer tok-alice")
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = api.Get("/users", "Authorization: Bearer bad")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestHumaMiddleware_CasbinPathMethod(t *testing.T) {
	enf, err := authz.NewMemoryEnforcer()
	if err != nil {
		t.Fatal(err)
	}
	if err := enf.AddPolicies([]authz.Policy{
		{Sub: "admin", Obj: "/users", Act: "GET"},
		{Sub: "admin", Obj: "/users/{id}", Act: "*"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := enf.AddRoleBindings([]authz.RoleBinding{{User: "alice", Role: "admin"}}); err != nil {
		t.Fatal(err)
	}

	api := installMiddleware(t, authz.HumaMiddlewareConfig{
		Enabled:       true,
		DevHeaderUser: true,
		Enforcer:      enf,
		Routes: []authz.HTTPRoute{
			{Method: "GET", Path: "/users"},
			{Method: "POST", Path: "/users"},
			{Method: "GET", Path: "/users/{id}"},
		},
	})

	if resp := api.Get("/users", "X-User: alice"); resp.Code != http.StatusOK {
		t.Fatalf("alice list: status=%d body=%s", resp.Code, resp.Body.String())
	}
	if resp := api.Get("/users/u1", "X-User: alice"); resp.Code != http.StatusOK {
		t.Fatalf("alice get: status=%d body=%s", resp.Code, resp.Body.String())
	}
	if resp := api.Post("/users", "X-User: alice", map[string]any{}); resp.Code != http.StatusForbidden {
		t.Fatalf("alice create denied: status=%d body=%s", resp.Code, resp.Body.String())
	}
	if resp := api.Get("/users", "X-User: bob"); resp.Code != http.StatusForbidden {
		t.Fatalf("bob denied: status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestHumaMiddleware_AutoBindBootstrap(t *testing.T) {
	enf, err := authz.NewMemoryEnforcer()
	if err != nil {
		t.Fatal(err)
	}
	if err := enf.AddPolicies([]authz.Policy{
		{Sub: "admin", Obj: "/users", Act: "GET"},
	}); err != nil {
		t.Fatal(err)
	}

	api := installMiddleware(t, authz.HumaMiddlewareConfig{
		Enabled:           true,
		DevHeaderUser:     true,
		AutoBindBootstrap: true,
		BootstrapRole:     "admin",
		Enforcer:          enf,
		Routes:            []authz.HTTPRoute{{Method: "GET", Path: "/users"}},
	})

	if resp := api.Get("/users", "X-User: logto-user-1"); resp.Code != http.StatusOK {
		t.Fatalf("auto-bind first call: status=%d body=%s", resp.Code, resp.Body.String())
	}
	roles, err := enf.ListSubjectRoles(context.Background(), "logto-user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf("roles=%v", roles)
	}
}

func TestSubject_Has(t *testing.T) {
	s := authz.Subject{Permissions: []string{"users:list", "orders:*"}}
	if !s.Has("users:list") || s.Has("users:create") {
		t.Fatalf("exact match failed: %+v", s.Permissions)
	}
	if !s.Has("orders:read") || !s.Has("orders:write") {
		t.Fatalf("prefix wildcard failed")
	}
	admin := authz.Subject{Permissions: []string{"*"}}
	if !admin.Has("anything") {
		t.Fatal("star wildcard failed")
	}
}

func TestHumaPathToCasbin(t *testing.T) {
	if got := authz.HumaPathToCasbin("/users/{id}"); got != "/users/:id" {
		t.Fatalf("got %q", got)
	}
	if got := authz.HumaPathToCasbin("/users"); got != "/users" {
		t.Fatalf("got %q", got)
	}
}
