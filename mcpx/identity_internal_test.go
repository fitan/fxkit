package mcpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildHTTPRequest_IdentityOverridesStaticHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer user-token")
	h.Set("X-User", "alice")
	h.Set("X-Org", "org-1")
	ctx := withIdentityHeaders(context.Background(), h)

	tool := Tool{Name: "ping", Method: http.MethodGet, Path: "/ping"}
	req, err := buildHTTPRequest(ctx, tool, map[string]string{
		"Authorization": "Bearer static-service-token",
		"X-Static":      "1",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer user-token" {
		t.Fatalf("Authorization=%q, caller identity must win", got)
	}
	if got := req.Header.Get("X-User"); got != "alice" {
		t.Fatalf("X-User=%q", got)
	}
	if got := req.Header.Get("X-Org"); got != "org-1" {
		t.Fatalf("X-Org=%q", got)
	}
	if got := req.Header.Get("X-Static"); got != "1" {
		t.Fatalf("static header lost: %q", got)
	}
}

func TestIdentityContextFromRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/sse", nil)
	r.Header.Set("Authorization", "Bearer t")
	r.Header.Set("X-User", "bob")
	// 未设置 X-Org 时不注入该键。
	ctx := identityContextFromRequest(context.Background(), r)
	h := identityHeadersFromContext(ctx)
	if h.Get("Authorization") != "Bearer t" || h.Get("X-User") != "bob" {
		t.Fatalf("headers=%v", h)
	}
	if _, ok := h["X-Org"]; ok {
		t.Fatalf("empty X-Org should not be injected: %v", h)
	}
}
