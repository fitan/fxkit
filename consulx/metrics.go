package consulx

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 分布式锁指标：获取耗时/结果、锁丢失次数、续租失败次数。
//
// 不把 lock key 作为属性（业务 key 基数可能很高）。
var (
	lockMetricsOnce     sync.Once
	lockAcquireDuration metric.Float64Histogram
	lockAcquireResults  metric.Int64Counter
	lockLostTotal       metric.Int64Counter
	lockRenewFailures   metric.Int64Counter
)

func initLockMetrics() {
	lockMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/consulx")
		lockAcquireDuration, _ = m.Float64Histogram(
			"consulx.lock.acquire.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Time to acquire a distributed lock"),
		)
		lockAcquireResults, _ = m.Int64Counter(
			"consulx.lock.acquire.results",
			metric.WithDescription("Lock acquire outcomes"),
		)
		lockLostTotal, _ = m.Int64Counter(
			"consulx.lock.lost",
			metric.WithDescription("Locks lost while held (session expiry / renew failures)"),
		)
		lockRenewFailures, _ = m.Int64Counter(
			"consulx.lock.renew.failures",
			metric.WithDescription("Session renew failures"),
		)
	})
}

func acquireResult(err error) string {
	switch {
	case err == nil:
		return "acquired"
	case errors.Is(err, ErrLockHeld):
		return "held"
	default:
		return "error"
	}
}

func recordLockAcquire(ctx context.Context, d time.Duration, err error) {
	initLockMetrics()
	attrs := metric.WithAttributes(attribute.String("lock.result", acquireResult(err)))
	if lockAcquireDuration != nil {
		lockAcquireDuration.Record(ctx, d.Seconds(), attrs)
	}
	if lockAcquireResults != nil {
		lockAcquireResults.Add(ctx, 1, attrs)
	}
}

func recordLockLost(ctx context.Context) {
	initLockMetrics()
	if lockLostTotal != nil {
		lockLostTotal.Add(ctx, 1)
	}
}

func recordRenewFailure(ctx context.Context) {
	initLockMetrics()
	if lockRenewFailures != nil {
		lockRenewFailures.Add(ctx, 1)
	}
}
