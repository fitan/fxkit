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
	// readyzMetricsOnce 保证 /readyz 指标只注册一次。
	readyzMetricsOnce sync.Once
	// readyzDuration 记录单次 /readyz 检查的耗时（单位秒）。
	readyzDuration metric.Float64Histogram
	// readyzChecks 统计 /readyz 检查结果（ok / error）。
	readyzChecks metric.Int64Counter
	// rejectionMetricsOnce 保证拒绝计数指标只注册一次。
	rejectionMetricsOnce sync.Once
	// rejections 统计被内置中间件拒绝的请求数（CSRF / 请求体超限）。
	rejections metric.Int64Counter
)

// initReadyzMetrics 惰性注册 /readyz 的耗时直方图与检查计数指标，只执行一次。
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

// initRejectionMetrics 注册中间件拒绝请求的计数（CSRF / 请求体超限）。
func initRejectionMetrics() {
	rejectionMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/server")
		rejections, _ = m.Int64Counter(
			"server.rejections",
			metric.WithDescription("Requests rejected by built-in middleware"),
		)
	})
}

// recordRejection 记录一次中间件拒绝，reason 标识来源（如 body_too_large）。
func recordRejection(ctx context.Context, reason string) {
	initRejectionMetrics()
	if rejections == nil {
		return
	}
	rejections.Add(ctx, 1, metric.WithAttributes(attribute.String("server.reason", reason)))
}

// recordReadyz 记录单次 /readyz 检查的耗时与结果（ok / error）。
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
