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
	// startupMetricsOnce 保证指标只注册一次。
	startupMetricsOnce sync.Once
	// startupDuration 是 fxkit.startup.duration 直方图，单位为秒，带 phase / result 属性。
	startupDuration metric.Float64Histogram
	// startupResults 是 fxkit.startup.results 计数器，带 phase / result 属性。
	startupResults metric.Int64Counter
)

// initStartupMetrics 用 sync.Once 惰性创建启动/停机耗时直方图与结果计数器。
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

// recordStartup 记录一次 start/stop 的耗时（秒）与结果（ok/error），并带上 phase 属性。
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
