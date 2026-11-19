package config

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 热更新指标：Consul watch 的拉取/解码/应用结果。
//
// key 是配置段名（有限集合，由调用方传入），可作为属性。
var (
	// configMetricsOnce 保证指标只注册一次。
	configMetricsOnce sync.Once
	// reloadResults 统计 watch 事件结果（stage × result）。
	reloadResults metric.Int64Counter
)

// initConfigMetrics 用 sync.Once 惰性注册热更新指标，重复调用无副作用。
func initConfigMetrics() {
	configMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/config")
		reloadResults, _ = m.Int64Counter(
			"config.reload",
			metric.WithDescription("Dynamic config watch outcomes"),
		)
	})
}

// recordReload 记录一次 watch 事件：stage=fetch|decode|apply，result=ok|error。
func recordReload(ctx context.Context, section, stage string, err error) {
	initConfigMetrics()
	if reloadResults == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	reloadResults.Add(ctx, 1, metric.WithAttributes(
		attribute.String("config.section", section),
		attribute.String("config.stage", stage),
		attribute.String("config.result", result),
	))
}
