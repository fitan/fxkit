package webhook

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 验签指标：失败原因（签名/时间戳/密钥）是安全信号，成功用于算拒绝率。
//
// 不记录签名、时间戳或请求体，避免敏感信息与高基数。
var (
	// webhookMetricsOnce 保证指标只注册一次。
	webhookMetricsOnce sync.Once
	// verifyResults 是 webhook.verifications 计数器，按 webhook.result 属性区分验签结果。
	verifyResults metric.Int64Counter
)

// initWebhookMetrics 用 sync.Once 惰性注册验签指标。
func initWebhookMetrics() {
	webhookMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/webhook")
		verifyResults, _ = m.Int64Counter(
			"webhook.verifications",
			metric.WithDescription("Webhook signature verification outcomes"),
		)
	})
}

// recordVerification 记录一次验签结果，result 取 ok / missing_secret / invalid_signature /
// missing_timestamp / invalid_timestamp / expired。
func recordVerification(result string) {
	initWebhookMetrics()
	if verifyResults == nil {
		return
	}
	verifyResults.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("webhook.result", result),
	))
}
