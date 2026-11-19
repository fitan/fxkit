package otelx

import (
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
)

// runtimeMetricsOnce 保证 runtime 指标只注册一次。
var runtimeMetricsOnce sync.Once

// runtimeMetricsErr 保存首次注册 runtime 指标的错误，后续调用原样返回。
var runtimeMetricsErr error

// startRuntimeMetrics 向全局 MeterProvider 注册 Go runtime metrics。
// 可安全多次调用；插桩仅注册一次。
func startRuntimeMetrics() error {
	runtimeMetricsOnce.Do(func() {
		runtimeMetricsErr = runtime.Start()
	})
	return runtimeMetricsErr
}
