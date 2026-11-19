package outbox

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 业务指标：发布耗时、结果、事件滞后（从入队到发布成功的时间）、批大小。
//
// 使用全局 MeterProvider（otelx 安装）；未启用时是 noop。
var (
	// outboxMetricsOnce 保证 outbox 指标只注册一次。
	outboxMetricsOnce sync.Once
	// publishDuration 记录 outbox.publish.duration（单条投递耗时，单位秒）。
	publishDuration metric.Float64Histogram
	// publishResults 统计 outbox.publish.results（published / failed）。
	publishResults metric.Int64Counter
	// eventLag 记录 outbox.event.lag（从 CreatedAt 到发布成功的时长，单位秒）。
	eventLag metric.Float64Histogram
	// batchSize 记录 outbox.batch.size（每轮认领的事件数）。
	batchSize metric.Int64Histogram
	// inboxDuration 记录 outbox.inbox.duration（单次 [Inbox.Once] 耗时，单位秒）。
	inboxDuration metric.Float64Histogram
	// inboxResults 统计 outbox.inbox.results（ok / skipped / busy / error）。
	inboxResults metric.Int64Counter
	// claimResults 统计 outbox.claim.results（ok / error）。
	claimResults metric.Int64Counter
	// heartbeatResults 统计 outbox.heartbeat.results（ok / error）。
	heartbeatResults metric.Int64Counter
)

// initOutboxMetrics 惰性初始化 outbox 指标（只执行一次）。
func initOutboxMetrics() {
	outboxMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/outbox")
		publishDuration, _ = m.Float64Histogram(
			"outbox.publish.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Time spent publishing one event"),
		)
		publishResults, _ = m.Int64Counter(
			"outbox.publish.results",
			metric.WithDescription("Publish outcomes"),
		)
		eventLag, _ = m.Float64Histogram(
			"outbox.event.lag",
			metric.WithUnit("s"),
			metric.WithDescription("Time from enqueue to publish success"),
		)
		batchSize, _ = m.Int64Histogram(
			"outbox.batch.size",
			metric.WithDescription("Number of events claimed per poll"),
		)
		inboxDuration, _ = m.Float64Histogram(
			"outbox.inbox.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Time spent in one inbox Once call"),
		)
		inboxResults, _ = m.Int64Counter(
			"outbox.inbox.results",
			metric.WithDescription("Inbox idempotency outcomes"),
		)
		claimResults, _ = m.Int64Counter(
			"outbox.claim.results",
			metric.WithDescription("Relay claim outcomes"),
		)
		heartbeatResults, _ = m.Int64Counter(
			"outbox.heartbeat.results",
			metric.WithDescription("Relay lease heartbeat outcomes"),
		)
	})
}

// recordPublish 记录一次发布耗时与结果（published/failed）。
func recordPublish(ctx context.Context, d time.Duration, ok bool) {
	initOutboxMetrics()
	result := "failed"
	if ok {
		result = "published"
	}
	if publishDuration != nil {
		publishDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String("outbox.result", result)))
	}
	if publishResults != nil {
		publishResults.Add(ctx, 1, metric.WithAttributes(attribute.String("outbox.result", result)))
	}
}

// recordLag 记录事件从 CreatedAt 到现在的时长（只在发布成功时调用）。
func recordLag(ctx context.Context, d time.Duration) {
	initOutboxMetrics()
	if eventLag == nil {
		return
	}
	eventLag.Record(ctx, d.Seconds())
}

// recordBatch 记录单次轮询认领的事件数。
func recordBatch(ctx context.Context, n int) {
	initOutboxMetrics()
	if batchSize == nil {
		return
	}
	batchSize.Record(ctx, int64(n))
}

// recordInbox 记录一次 [Inbox.Once] 的结果（ok / skipped / busy / error）。
func recordInbox(ctx context.Context, result string, d time.Duration) {
	initOutboxMetrics()
	attrs := metric.WithAttributes(attribute.String("outbox.result", result))
	if inboxDuration != nil {
		inboxDuration.Record(ctx, d.Seconds(), attrs)
	}
	if inboxResults != nil {
		inboxResults.Add(ctx, 1, attrs)
	}
}

// recordClaim 记录一次认领结果（ok/error）。
func recordClaim(ctx context.Context, err error) {
	initOutboxMetrics()
	if claimResults == nil {
		return
	}
	claimResults.Add(ctx, 1, metric.WithAttributes(attribute.String("outbox.result", boolResult(err))))
}

// recordHeartbeat 记录一次租约续期结果（ok/error）。
func recordHeartbeat(ctx context.Context, err error) {
	initOutboxMetrics()
	if heartbeatResults == nil {
		return
	}
	heartbeatResults.Add(ctx, 1, metric.WithAttributes(attribute.String("outbox.result", boolResult(err))))
}

// boolResult 把 error 映射为指标标签：nil 为 ok，否则 error。
func boolResult(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
