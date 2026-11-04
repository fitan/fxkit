package authz

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 业务指标：Casbin 判定耗时与结果（allow / deny / error）。
var (
	authzMetricsOnce sync.Once
	enforceDuration  metric.Float64Histogram
	enforceResults   metric.Int64Counter
)

func initAuthzMetrics() {
	authzMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/authz")
		enforceDuration, _ = m.Float64Histogram(
			"authz.enforce.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Casbin enforcement duration"),
		)
		enforceResults, _ = m.Int64Counter(
			"authz.enforce.results",
			metric.WithDescription("Casbin enforcement outcomes"),
		)
	})
}

func recordEnforce(ctx context.Context, d time.Duration, allowed bool, err error) {
	initAuthzMetrics()
	result := "deny"
	switch {
	case err != nil:
		result = "error"
	case allowed:
		result = "allow"
	}
	if enforceDuration != nil {
		enforceDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String("authz.result", result)))
	}
	if enforceResults != nil {
		enforceResults.Add(ctx, 1, metric.WithAttributes(attribute.String("authz.result", result)))
	}
}
