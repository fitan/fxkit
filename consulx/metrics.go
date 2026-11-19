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
	// lockMetricsOnce 保证分布式锁指标只注册一次。
	lockMetricsOnce sync.Once
	// lockAcquireDuration 记录锁获取耗时（单位 s）。
	lockAcquireDuration metric.Float64Histogram
	// lockAcquireResults 统计锁获取结果（acquired/held/error）。
	lockAcquireResults metric.Int64Counter
	// lockLostTotal 统计持有期间丢失的锁数量。
	lockLostTotal metric.Int64Counter
	// lockRenewFailures 统计会话续租失败次数。
	lockRenewFailures metric.Int64Counter

	// registerMetricsOnce 保证注册相关指标只注册一次。
	registerMetricsOnce sync.Once
	// registerTTLResults 统计 TTL 心跳结果（ok/error）。
	registerTTLResults metric.Int64Counter
	// reregisterResults 统计 TTL 失败后重新注册的结果（ok/error）。
	reregisterResults metric.Int64Counter
)

// initLockMetrics 用 sync.Once 注册分布式锁指标，重复调用无副作用。
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

// initRegisterMetrics 注册服务注册/心跳指标（TTL pass 与重新注册）。
func initRegisterMetrics() {
	registerMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/consulx")
		registerTTLResults, _ = m.Int64Counter(
			"consulx.register.ttl",
			metric.WithDescription("TTL heartbeat outcomes"),
		)
		reregisterResults, _ = m.Int64Counter(
			"consulx.register.reregister",
			metric.WithDescription("Service re-registration outcomes after TTL failures"),
		)
	})
}

// recordTTLPass 记录一次 TTL 心跳结果（ok / error）。
func recordTTLPass(ctx context.Context, err error) {
	initRegisterMetrics()
	if registerTTLResults == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	registerTTLResults.Add(ctx, 1, metric.WithAttributes(attribute.String("consulx.result", result)))
}

// recordReregister 记录一次 TTL 失败后重新注册的结果（ok / error）。
func recordReregister(ctx context.Context, err error) {
	initRegisterMetrics()
	if reregisterResults == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	reregisterResults.Add(ctx, 1, metric.WithAttributes(attribute.String("consulx.result", result)))
}

// acquireResult 把获取结果映射为指标标签值：acquired / held / error。
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

// recordLockAcquire 记录一次获取耗时与结果计数。
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

// recordLockLost 锁丢失计数加一。
func recordLockLost(ctx context.Context) {
	initLockMetrics()
	if lockLostTotal != nil {
		lockLostTotal.Add(ctx, 1)
	}
}

// recordRenewFailure 续租失败计数加一。
func recordRenewFailure(ctx context.Context) {
	initLockMetrics()
	if lockRenewFailures != nil {
		lockRenewFailures.Add(ctx, 1)
	}
}
