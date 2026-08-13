package otelx

import (
	"sync"
	"testing"

	"github.com/fitan/fxkit/config"
)

func TestOtlpUsesHTTP(t *testing.T) {
	base := config.OtelConfig{Protocol: "grpc", Endpoint: "localhost:4317"}
	if otlpUsesHTTP(base, "otlp") {
		t.Fatal("grpc protocol should not use http")
	}
	httpCfg := config.OtelConfig{Protocol: "http", Endpoint: "localhost:4318"}
	if !otlpUsesHTTP(httpCfg, "otlp") {
		t.Fatal("http protocol should use http exporter")
	}
	if !otlpUsesHTTP(base, "otlp_http") {
		t.Fatal("otlp_http exporter should use http")
	}
}

func TestNormalizeOTLPEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://otel.example:4317/": "otel.example:4317",
		"http://localhost:4318":      "localhost:4318",
		"localhost:4317":             "localhost:4317",
		"  https://a:1  ":            "a:1",
	}
	for in, want := range cases {
		if got := normalizeOTLPEndpoint(in); got != want {
			t.Fatalf("normalizeOTLPEndpoint(%q)=%q want %q", in, got, want)
		}
	}
}

func TestStartRuntimeMetricsOnce(t *testing.T) {
	runtimeMetricsOnce = sync.Once{}
	runtimeMetricsErr = nil

	if err := startRuntimeMetrics(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := startRuntimeMetrics(); err != nil {
		t.Fatalf("second start: %v", err)
	}
}
