package hatchetx

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Actor 运行指标（actor 名是有限集合，可作为属性）。
var (
	// actorMetricsOnce 保证 actor 指标只注册一次。
	actorMetricsOnce sync.Once
	// actorRuns 按 actor 名与结果统计处理器调用次数。
	actorRuns metric.Int64Counter
	// actorDuration 记录处理器耗时（单位秒），带 actor 名与结果属性。
	actorDuration metric.Float64Histogram
)

// initActorMetrics 惰性注册 actor 运行次数与耗时指标，只执行一次。
func initActorMetrics() {
	actorMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/hatchetx")
		actorRuns, _ = m.Int64Counter(
			"hatchetx.actor.runs",
			metric.WithDescription("Semantic actor handler invocations"),
		)
		actorDuration, _ = m.Float64Histogram(
			"hatchetx.actor.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Semantic actor handler duration"),
		)
	})
}

// recordActorRun 记录一次 actor 处理的结果（ok / error）与耗时。
func recordActorRun(ctx context.Context, actor string, d time.Duration, failed bool) {
	initActorMetrics()
	result := "ok"
	if failed {
		result = "error"
	}
	attrs := metric.WithAttributes(
		attribute.String("hatchetx.actor", actor),
		attribute.String("hatchetx.result", result),
	)
	if actorRuns != nil {
		actorRuns.Add(ctx, 1, attrs)
	}
	if actorDuration != nil {
		actorDuration.Record(ctx, d.Seconds(), attrs)
	}
}
