package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCORS_NonCORSOPTIONSPassesThrough 回归：
// 没有 Origin 的 OPTIONS 是普通请求，必须交给路由决定 404/405，而不是被中间件伪造 200。
func TestCORS_NonCORSOPTIONSPassesThrough(t *testing.T) {
	h := corsMiddleware(&Config{CORSAllowedOrigins: []string{"http://localhost:3000"}})

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	req := httptest.NewRequest(http.MethodOptions, "/missing", nil)
	rec := httptest.NewRecorder()
	h(next).ServeHTTP(rec, req)

	if !called {
		t.Fatal("non-CORS OPTIONS must reach the router")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want 405 from router", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected CORS header %q", got)
	}
}

// TestCORS_DisallowedPreflightIsNotShortCircuited 回归：
// 不允许的 Origin 不能拿到预检成功响应。
func TestCORS_DisallowedPreflightIsNotShortCircuited(t *testing.T) {
	h := corsMiddleware(&Config{CORSAllowedOrigins: []string{"http://localhost:3000"}})

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusForbidden)
	})
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	h(next).ServeHTTP(rec, req)

	if !called {
		t.Fatal("disallowed preflight must not be answered by the CORS middleware")
	}
	if rec.Code == http.StatusOK {
		t.Fatal("disallowed preflight must not return 200")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected allow-origin %q", got)
	}
}
