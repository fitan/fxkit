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
	actorMetricsOnce sync.Once
	actorRuns        metric.Int64Counter
	actorDuration    metric.Float64Histogram
)

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
