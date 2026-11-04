package server

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// /readyz 指标：每项检查的耗时与状态。
//
// check 名称是有限集合（db / redis / ...），作为属性安全。
var (
	readyzMetricsOnce sync.Once
	readyzDuration    metric.Float64Histogram
	readyzChecks      metric.Int64Counter
)

func initReadyzMetrics() {
	readyzMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/server")
		readyzDuration, _ = m.Float64Histogram(
			"server.readyz.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Duration of a single /readyz health check"),
		)
		readyzChecks, _ = m.Int64Counter(
			"server.readyz.checks",
			metric.WithDescription("Readiness check outcomes"),
		)
	})
}

func recordReadyz(ctx context.Context, check string, d time.Duration, err error) {
	initReadyzMetrics()
	status := "ok"
	if err != nil {
		status = "error"
	}
	attrs := metric.WithAttributes(
		attribute.String("check", check),
		attribute.String("status", status),
	)
	if readyzDuration != nil {
		readyzDuration.Record(ctx, d.Seconds(), attrs)
	}
	if readyzChecks != nil {
		readyzChecks.Add(ctx, 1, attrs)
	}
}
