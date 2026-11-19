package reconciliation

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 业务指标：对账耗时、运行结果与各差异类型的条数。
//
// Kind 是有限枚举（missing_internal / missing_external / mismatch），可安全作为属性。
var (
	// reconciliationMetricsOnce 保证指标只注册一次。
	reconciliationMetricsOnce sync.Once
	// runResults 是 reconciliation.runs 计数器，按 reconciliation.result 属性区分 ok / error。
	runResults metric.Int64Counter
	// diffCounts 是 reconciliation.diffs 计数器，按 reconciliation.kind 属性累计差异条数。
	diffCounts metric.Int64Counter
	// runDuration 是 reconciliation.duration 直方图，单位为秒。
	runDuration metric.Float64Histogram
)

// initReconciliationMetrics 用 sync.Once 惰性创建运行次数、差异条数与耗时指标。
func initReconciliationMetrics() {
	reconciliationMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/reconciliation")
		runResults, _ = m.Int64Counter(
			"reconciliation.runs",
			metric.WithDescription("Reconciliation runs by outcome"),
		)
		diffCounts, _ = m.Int64Counter(
			"reconciliation.diffs",
			metric.WithDescription("Differences found by kind"),
		)
		runDuration, _ = m.Float64Histogram(
			"reconciliation.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Reconciliation run duration"),
		)
	})
}

// recordRun 记录一次对账的结果、耗时（秒），成功时按 kind 累加差异条数。
func recordRun[T any](ctx context.Context, diffs []Diff[T], err error, d time.Duration) {
	initReconciliationMetrics()
	result := "ok"
	if err != nil {
		result = "error"
	}
	attrs := metric.WithAttributes(attribute.String("reconciliation.result", result))
	if runResults != nil {
		runResults.Add(ctx, 1, attrs)
	}
	if runDuration != nil {
		runDuration.Record(ctx, d.Seconds(), attrs)
	}
	if err != nil || diffCounts == nil {
		return
	}
	for kind, n := range Summary(diffs) {
		diffCounts.Add(ctx, int64(n), metric.WithAttributes(
			attribute.String("reconciliation.kind", string(kind)),
		))
	}
}
