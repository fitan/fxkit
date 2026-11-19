package audit

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 业务指标：审计事件写入结果。
//
// sink 失败只打 Warn 日志时，审计丢失不会触发告警；这里用 error 计数暴露。
var (
	// auditMetricsOnce 保证指标只注册一次。
	auditMetricsOnce sync.Once
	// eventResults 是 audit.events 计数器，按 audit.result 属性区分 ok / error。
	eventResults metric.Int64Counter
)

// initAuditMetrics 用 sync.Once 惰性注册审计事件指标，重复调用无副作用。
func initAuditMetrics() {
	auditMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/audit")
		eventResults, _ = m.Int64Counter(
			"audit.events",
			metric.WithDescription("Audit event sink outcomes"),
		)
	})
}

// recordEvent 记录一次审计事件写入结果：err 为 nil 记 ok，否则记 error。
func recordEvent(ctx context.Context, err error) {
	initAuditMetrics()
	if eventResults == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	eventResults.Add(ctx, 1, metric.WithAttributes(attribute.String("audit.result", result)))
}
