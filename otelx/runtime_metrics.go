package otelx

import (
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
)

var runtimeMetricsOnce sync.Once
var runtimeMetricsErr error

// startRuntimeMetrics 向全局 MeterProvider 注册 Go runtime metrics。
// 可安全多次调用；插桩仅注册一次。
func startRuntimeMetrics() error {
	runtimeMetricsOnce.Do(func() {
		runtimeMetricsErr = runtime.Start()
	})
	return runtimeMetricsErr
}
