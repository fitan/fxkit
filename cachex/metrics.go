package cachex

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// cacheTracer 用于 cache.load span（不记录 key，避免高基数/敏感）。
var cacheTracer = otel.Tracer("github.com/fitan/fxkit/cachex")

// 业务指标：命中率与 loader 耗时。
//
// 使用全局 MeterProvider（otelx 在启动时安装），因此这里可以惰性创建；
// 未启用 OTel 时全局是 noop provider，开销极小。
var (
	cacheMetricsOnce    sync.Once
	cacheLookups        metric.Int64Counter
	cacheLoaderDuration metric.Float64Histogram
)

func initCacheMetrics() {
	cacheMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/cachex")
		cacheLookups, _ = m.Int64Counter(
			"cachex.lookups",
			metric.WithDescription("Cache lookups by outcome"),
		)
		cacheLoaderDuration, _ = m.Float64Histogram(
			"cachex.loader.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Loader duration when the cache misses"),
		)
	})
}

// recordLookup 记录一次缓存查询结果：hit / negative / miss / error / disabled。
func recordLookup(ctx context.Context, result string) {
	initCacheMetrics()
	if cacheLookups == nil {
		return
	}
	cacheLookups.Add(ctx, 1, metric.WithAttributes(attribute.String("cache.result", result)))
}

func recordLoader(ctx context.Context, d time.Duration) {
	initCacheMetrics()
	if cacheLoaderDuration == nil {
		return
	}
	cacheLoaderDuration.Record(ctx, d.Seconds())
}

// startLoadSpan 为一次回源创建 span（key 不入属性）。
func startLoadSpan(ctx context.Context) (context.Context, trace.Span) {
	return cacheTracer.Start(ctx, "cache.load", trace.WithAttributes(
		attribute.String("cache.operation", "get_or_set"),
	))
}

// endLoadSpan 根据 loader 结果标注 span。
func endLoadSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "load failed")
	}
	span.End()
}
