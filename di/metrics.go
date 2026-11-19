package di

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// PreStart 分阶段耗时：di.phase 是 [Lifecycle.AppendPreStartPhase] 的优先级，
// 取值来自 PhaseResolveContributors / PhaseWireGroup 等有限常量。
//
// 用于定位「启动慢在解析贡献点、固化 group，还是挂载路由」。
var (
	// lifecycleMetricsOnce 保证指标只注册一次。
	lifecycleMetricsOnce sync.Once
	// preStartDuration 记录各 PreStart 阶段的耗时，单位为秒。
	preStartDuration metric.Float64Histogram
)

// initLifecycleMetrics 用 sync.Once 惰性注册 PreStart 阶段耗时指标。
func initLifecycleMetrics() {
	lifecycleMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/di")
		preStartDuration, _ = m.Float64Histogram(
			"di.prestart.duration",
			metric.WithUnit("s"),
			metric.WithDescription("PreStart hook duration by phase priority"),
		)
	})
}

// recordPreStartPhase 记录一个 PreStart 阶段的耗时与结果，di.phase 属性是阶段优先级。
func recordPreStartPhase(ctx context.Context, priority int, d time.Duration, err error) {
	initLifecycleMetrics()
	if preStartDuration == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	preStartDuration.Record(ctx, d.Seconds(), metric.WithAttributes(
		attribute.Int("di.phase", priority),
		attribute.String("di.result", result),
	))
}
