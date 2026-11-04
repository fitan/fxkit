package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestAccessLogMiddleware 验证访问日志字段，并确认记录的是路由模板而非真实路径。
func TestAccessLogMiddleware(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	r := chi.NewRouter()
	r.Use(accessLogMiddleware)
	r.Get("/items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/items/42", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	out := buf.String()
	for _, want := range []string{
		"msg=\"http request\"",
		"method=GET",
		"route=/items/{id}",
		"status=201",
		"bytes=2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("access log missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/items/42") {
		t.Fatalf("access log must use route template, not raw path:\n%s", out)
	}
}

// TestAccessLogMiddlewareUnmatched 未匹配路由标记为 unmatched（避免空 route）。
func TestAccessLogMiddlewareUnmatched(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	h := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if out := buf.String(); !strings.Contains(out, "route=unmatched") {
		t.Fatalf("expected unmatched route:\n%s", out)
	}
}
