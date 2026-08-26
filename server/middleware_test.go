package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fitan/fxkit/otelx"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestNewMux_otelMiddlewareGated(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })

	enabledMux := NewMux(MuxParams{
		Server: &Config{},
		Otel:   &otelx.Config{Enabled: true, Traces: otelx.TracesConfig{Enabled: true}},
	})
	enabledMux.Get("/probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec.Reset()
	enabledMux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))
	if n := countHTTPServerSpans(rec.Ended()); n != 1 {
		t.Fatalf("otel enabled: got %d http server spans, want 1", n)
	}

	disabledMux := NewMux(MuxParams{
		Server: &Config{},
		Otel:   &otelx.Config{Enabled: false, Traces: otelx.TracesConfig{Enabled: true}},
	})
	disabledMux.Get("/probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec.Reset()
	disabledMux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))
	if n := countHTTPServerSpans(rec.Ended()); n != 0 {
		t.Fatalf("otel disabled: got %d http server spans, want 0", n)
	}
}

func countHTTPServerSpans(spans []sdktrace.ReadOnlySpan) int {
	n := 0
	for _, s := range spans {
		if s.SpanContext().IsValid() {
			n++
		}
	}
	return n
}

func TestRequestBodyLogMiddleware_DoesNotDropByte(t *testing.T) {
	const maxLoggedBytes = 8 * 1024
	want := bytes.Repeat([]byte("x"), maxLoggedBytes+50)
	var got []byte
	h := requestBodyLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		got, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/users", bytes.NewReader(want))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !bytes.Equal(got, want) {
		t.Fatalf("body len=%d want=%d (dropped %d bytes)", len(got), len(want), len(want)-len(got))
	}
}

func TestCORS_AllowsPATCH(t *testing.T) {
	h := corsMiddleware([]string{"http://localhost:3000"})
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	rec := httptest.NewRecorder()
	h(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	allow := rec.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(allow, "PATCH") {
		t.Fatalf("Allow-Methods=%q", allow)
	}
}

func TestRoutePattern_SetsSpanName(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })

	mux := NewMux(MuxParams{
		Server: &Config{},
		Otel:   &otelx.Config{Enabled: true, Traces: otelx.TracesConfig{Enabled: true}},
	})
	mux.Get("/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec.Reset()
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/42", nil))
	found := false
	for _, s := range rec.Ended() {
		if s.Name() == "GET /users/{id}" {
			found = true
			break
		}
	}
	if !found {
		var names []string
		for _, s := range rec.Ended() {
			names = append(names, s.Name())
		}
		t.Fatalf("span names=%v want GET /users/{id}", names)
	}
}
