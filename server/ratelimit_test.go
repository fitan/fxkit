package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func rateLimitProbe(h http.Handler, remote string) int {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestRateLimitMiddlewareLimitsBurst(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	// 几乎不补充令牌：只有 burst=1 的第一次请求能过。
	h := rateLimitMiddleware(RateLimitConfig{Enabled: true, RPS: 0.0001, Burst: 1})(next)

	if got := rateLimitProbe(h, "1.2.3.4:1111"); got != http.StatusOK {
		t.Fatalf("first request = %d, want 200", got)
	}
	if got := rateLimitProbe(h, "1.2.3.4:1111"); got != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", got)
	}
	// 不同 IP 独立计数。
	if got := rateLimitProbe(h, "5.6.7.8:2222"); got != http.StatusOK {
		t.Fatalf("other ip = %d, want 200", got)
	}
}

func TestRateLimitMiddlewareDisabledPassesThrough(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := rateLimitMiddleware(RateLimitConfig{})(next)
	for i := 0; i < 5; i++ {
		if got := rateLimitProbe(h, "1.2.3.4:1111"); got != http.StatusOK {
			t.Fatalf("request %d = %d, want 200 (disabled)", i, got)
		}
	}
}

func TestClientIPIgnoresForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "1.1.1.1")
	if got := clientIP(req); got != "10.0.0.1" {
		t.Fatalf("clientIP = %q, want 10.0.0.1", got)
	}
}
