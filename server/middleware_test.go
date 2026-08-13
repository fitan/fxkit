package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fitan/fxkit/config"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func testConfig(t *testing.T, httpOtel bool) *config.Config {
	t.Helper()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := cfg.Viper()
	v.Set("otel.enabled", httpOtel)
	v.Set("otel.traces.enabled", httpOtel)
	if err := cfg.Sync(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestNewMux_otelMiddlewareGated(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })

	enabledMux := NewMux(MuxParams{Config: testConfig(t, true)})
	enabledMux.Get("/probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec.Reset()
	enabledMux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))
	if n := countHTTPServerSpans(rec.Ended()); n != 1 {
		t.Fatalf("otel enabled: got %d http server spans, want 1", n)
	}

	disabledMux := NewMux(MuxParams{Config: testConfig(t, false)})
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
