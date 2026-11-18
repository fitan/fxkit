package authz

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func newMuxAuthzHandler(t *testing.T) http.Handler {
	t.Helper()

	enf, err := NewMemoryEnforcer()
	require.NoError(t, err)
	require.NoError(t, enf.AddRoleBindings([]RoleBinding{{User: "alice", Role: "admin"}}))
	require.NoError(t, enf.AddPolicies([]Policy{{Sub: "admin", Obj: "/secret", Act: http.MethodGet}}))

	mw := NewMuxMiddleware(MuxMiddlewareConfig{
		Enabled:       true,
		DevHeaderUser: true, DevOrgID: DefaultDomain,
		Enforcer:    enf,
		SubjectFunc: DefaultDevSubjectFunc,
		PublicPaths: []string{"/public"},
	})
	return mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subj, _ := SubjectFromContext(r.Context())
		_, _ = w.Write([]byte(subj.ID))
	}))
}

func doMuxReq(h http.Handler, method, path, user string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if user != "" {
		req.Header.Set("X-User", user)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMuxMiddlewareAuthzMatrix(t *testing.T) {
	h := newMuxAuthzHandler(t)

	if rec := doMuxReq(h, http.MethodGet, "/secret", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d, want 401", rec.Code)
	}
	rec := doMuxReq(h, http.MethodGet, "/secret", "alice")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "alice", rec.Body.String(), "Subject 必须写入 request context")
	if rec := doMuxReq(h, http.MethodGet, "/secret", "bob"); rec.Code != http.StatusForbidden {
		t.Fatalf("bob: %d, want 403", rec.Code)
	}
}

func TestMuxMiddlewarePublicPaths(t *testing.T) {
	h := newMuxAuthzHandler(t)

	if rec := doMuxReq(h, http.MethodGet, "/public/ping", ""); rec.Code != http.StatusOK {
		t.Fatalf("public path: %d, want 200", rec.Code)
	}
	// 内置文档/schema 路径始终公开（docs 是否挂载由 docs.enabled 决定）。
	if rec := doMuxReq(h, http.MethodGet, "/docs", ""); rec.Code != http.StatusOK {
		t.Fatalf("/docs: %d, want 200", rec.Code)
	}
}

func TestMuxMiddlewareDisabledPassesThrough(t *testing.T) {
	mw := NewMuxMiddleware(MuxMiddlewareConfig{Enabled: false})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	if rec := doMuxReq(h, http.MethodGet, "/secret", ""); rec.Code != http.StatusOK {
		t.Fatalf("disabled: %d, want 200", rec.Code)
	}
}
