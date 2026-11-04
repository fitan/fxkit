package otelx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/fitan/fxkit/di"
	"go.opentelemetry.io/otel"
)

// TestSetupExportsSpansOverOTLPHTTP 真实走一遍「配置 → exporter → HTTP 上报」，
// 用本地 httptest 充当 OTLP collector，验证 span 确实被导出。
func TestSetupExportsSpansOverOTLPHTTP(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			hits.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// Setup 会改全局 provider 与 slog default，测试后恢复。
	prevTP := otel.GetTracerProvider()
	prevLogger := slog.Default()
	defer func() {
		otel.SetTracerProvider(prevTP)
		slog.SetDefault(prevLogger)
	}()

	cfg := &Config{
		Enabled:     true,
		ServiceName: "itest",
		Environment: "test",
		Endpoint:    u.Host,
		Protocol:    "http",
		Insecure:    true,
		Sampling:    "always_on",
		Traces:      TracesConfig{Enabled: true, Exporter: "otlp"},
	}
	lc := di.NewLifecycle()
	if err := Setup(lc, cfg, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, span := otel.Tracer("itest").Start(context.Background(), "op")
	span.End()

	// Shutdown 会 flush batcher。
	if err := lc.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("OTLP collector received no trace payload")
	}
}
