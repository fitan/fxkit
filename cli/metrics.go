package cli

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 启动/停机耗时指标，便于定位慢启动（DB 连接、Huma registrar、迁移等）。
var (
	startupMetricsOnce sync.Once
	startupDuration    metric.Float64Histogram
	startupResults     metric.Int64Counter
)

func initStartupMetrics() {
	startupMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/cli")
		startupDuration, _ = m.Float64Histogram(
			"fxkit.startup.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Lifecycle start/stop duration by phase"),
		)
		startupResults, _ = m.Int64Counter(
			"fxkit.startup.results",
			metric.WithDescription("Lifecycle start/stop outcomes"),
		)
	})
}

func recordStartup(phase string, d time.Duration, err error) {
	initStartupMetrics()
	result := "ok"
	if err != nil {
		result = "error"
	}
	attrs := metric.WithAttributes(
		attribute.String("phase", phase),
		attribute.String("result", result),
	)
	if startupDuration != nil {
		startupDuration.Record(context.Background(), d.Seconds(), attrs)
	}
	if startupResults != nil {
		startupResults.Add(context.Background(), 1, attrs)
	}
}
