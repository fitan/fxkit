package ledger

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 业务指标：记账结果（ok / unbalanced / invalid / conflict / error）、分录数与耗时。
var (
	// ledgerMetricsOnce 保证指标只注册一次。
	ledgerMetricsOnce sync.Once
	// postResults 是 ledger.posts 计数器，按 ledger.result 属性区分记账结果。
	postResults metric.Int64Counter
	// postingsCount 是 ledger.postings.count 直方图，记录每笔事务的分录数。
	postingsCount metric.Int64Histogram
	// postDuration 是 ledger.post.duration 直方图，单位为秒。
	postDuration metric.Float64Histogram
)

// initLedgerMetrics 用 sync.Once 惰性注册记账指标，重复调用无副作用。
func initLedgerMetrics() {
	ledgerMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/ledger")
		postResults, _ = m.Int64Counter(
			"ledger.posts",
			metric.WithDescription("Ledger posting outcomes"),
		)
		postingsCount, _ = m.Int64Histogram(
			"ledger.postings.count",
			metric.WithDescription("Postings per ledger transaction"),
		)
		postDuration, _ = m.Float64Histogram(
			"ledger.post.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Ledger Post duration"),
		)
	})
}

// recordPost 记录一次记账结果、分录数与耗时；result 取 ok / invalid / unbalanced / conflict / error。
func recordPost(ctx context.Context, result string, postings int, d time.Duration) {
	initLedgerMetrics()
	attrs := metric.WithAttributes(attribute.String("ledger.result", result))
	if postResults != nil {
		postResults.Add(ctx, 1, attrs)
	}
	if postingsCount != nil {
		postingsCount.Record(ctx, int64(postings))
	}
	if postDuration != nil {
		postDuration.Record(ctx, d.Seconds(), attrs)
	}
}
