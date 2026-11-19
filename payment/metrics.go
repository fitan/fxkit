package payment

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// 业务指标与 span：状态流转结果（ok / invalid / conflict / error）。
//
// from/to 是业务状态名，基数受状态机规模约束；result 是有限枚举。
var (
	// paymentMetricsOnce 保证指标只注册一次。
	paymentMetricsOnce sync.Once
	// transitions 是 payment.transitions 计数器，按 payment.result 属性区分流转结果。
	transitions metric.Int64Counter
	// paymentTracer 是 payment.apply span 的 tracer。
	paymentTracer = otel.Tracer("github.com/fitan/fxkit/payment")
)

// initPaymentMetrics 用 sync.Once 惰性创建 payment.transitions 计数器。
func initPaymentMetrics() {
	paymentMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/payment")
		transitions, _ = m.Int64Counter(
			"payment.transitions",
			metric.WithDescription("Payment state machine transitions"),
		)
	})
}

// startApplySpan 开启 payment.apply span，并带上 from/to 状态属性。
func startApplySpan(ctx context.Context, from, to State) (context.Context, trace.Span) {
	return paymentTracer.Start(ctx, "payment.apply", trace.WithAttributes(
		attribute.String("payment.from", string(from)),
		attribute.String("payment.to", string(to)),
	))
}

// endApplySpan 按 result 累加一次流转计数，把错误记录到 span 后结束它。
func endApplySpan(ctx context.Context, span trace.Span, result string, err error) {
	initPaymentMetrics()
	if transitions != nil {
		transitions.Add(ctx, 1, metric.WithAttributes(attribute.String("payment.result", result)))
	}
	span.SetAttributes(attribute.String("payment.result", result))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, result)
	}
	span.End()
}
