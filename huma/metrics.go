package huma

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/fitan/fxkit/fxerrors"
)

// 错误指标：按 fxerrors.Kind 与映射后的 HTTP status 计数。
//
// otelhttp 只给到 status；这里补上 kind，便于区分「校验失败」「鉴权失败」「内部错误」。
// AsError 没有 context，使用 Background 记录（指标本身不需要 span 关联）。
var (
	// humaMetricsOnce 保证 huma 错误指标只注册一次。
	humaMetricsOnce sync.Once
	// errorResults 统计 huma.errors（按 fxerrors.Kind 与映射后的 HTTP status）。
	errorResults metric.Int64Counter
)

// initHumaMetrics 用 sync.Once 惰性注册错误指标。
func initHumaMetrics() {
	humaMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/huma")
		errorResults, _ = m.Int64Counter(
			"huma.errors",
			metric.WithDescription("Handler errors by fxerrors kind and HTTP status"),
		)
	})
}

// recordError 按 fxerrors.Kind 及其映射的 HTTP status 记录一次 handler 错误。
func recordError(kind fxerrors.Kind) {
	initHumaMetrics()
	if errorResults == nil {
		return
	}
	errorResults.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("error.kind", string(kind)),
		attribute.Int("http.status", kind.Status()),
	))
}
