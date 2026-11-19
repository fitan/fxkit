package reqx

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 出站指标：一次逻辑请求的最终结果与实际尝试的端点数。
//
// 不把服务名/端点作为属性：服务名虽有限，但端点会随 Consul 变化，容易变成高基数。
var (
	// reqxMetricsOnce 保证出站指标只注册一次。
	reqxMetricsOnce sync.Once
	// requestResults 按结果（ok/error/no_endpoints）统计出站请求数。
	requestResults metric.Int64Counter
	// requestAttempts 记录每个出站请求实际尝试的端点数。
	requestAttempts metric.Int64Histogram
)

// initReqxMetrics 惰性初始化出站请求指标（只执行一次）。
func initReqxMetrics() {
	reqxMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/reqx")
		requestResults, _ = m.Int64Counter(
			"reqx.requests",
			metric.WithDescription("Outbound requests by outcome"),
		)
		requestAttempts, _ = m.Int64Histogram(
			"reqx.attempts",
			metric.WithDescription("Endpoints attempted per outbound request"),
		)
	})
}

// recordRequest 记录一次 RoundTrip 的结果：ok / error / no_endpoints。
func recordRequest(ctx context.Context, result string, attempts int) {
	initReqxMetrics()
	if requestResults != nil {
		requestResults.Add(ctx, 1, metric.WithAttributes(attribute.String("reqx.result", result)))
	}
	if requestAttempts != nil {
		requestAttempts.Record(ctx, int64(attempts))
	}
}
