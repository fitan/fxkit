package idempotency

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 业务指标：请求结果（首次 / 重放 / 冲突 / 错误）与 store 操作耗时。
//
// 使用全局 MeterProvider（otelx 安装）；未启用时是 noop。
var (
	// idempotencyMetricsOnce 保证指标只注册一次。
	idempotencyMetricsOnce sync.Once
	// requestResults 统计中间件结果（first / replayed / conflict / error）。
	requestResults metric.Int64Counter
	// storeDuration 记录 store 操作耗时，单位为秒。
	storeDuration metric.Float64Histogram
	// storeResults 统计 store 操作结果（ok / error）。
	storeResults metric.Int64Counter
)

// initIdempotencyMetrics 惰性初始化幂等指标（只执行一次）。
func initIdempotencyMetrics() {
	idempotencyMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/idempotency")
		requestResults, _ = m.Int64Counter(
			"idempotency.requests",
			metric.WithDescription("Idempotency middleware outcomes"),
		)
		storeDuration, _ = m.Float64Histogram(
			"idempotency.store.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Idempotency store operation duration"),
		)
		storeResults, _ = m.Int64Counter(
			"idempotency.store.results",
			metric.WithDescription("Idempotency store operation outcomes"),
		)
	})
}

// recordRequest 记录一次中间件结果：first / replayed / conflict / error。
func recordRequest(ctx context.Context, result string) {
	initIdempotencyMetrics()
	if requestResults == nil {
		return
	}
	requestResults.Add(ctx, 1, metric.WithAttributes(attribute.String("idempotency.result", result)))
}

// recordStoreOp 记录 store 的 begin/complete/abort 耗时与结果。
func recordStoreOp(ctx context.Context, op string, d time.Duration, err error) {
	initIdempotencyMetrics()
	result := "ok"
	if err != nil {
		result = "error"
	}
	attrs := metric.WithAttributes(
		attribute.String("idempotency.op", op),
		attribute.String("idempotency.result", result),
	)
	if storeDuration != nil {
		storeDuration.Record(ctx, d.Seconds(), attrs)
	}
	if storeResults != nil {
		storeResults.Add(ctx, 1, attrs)
	}
}
