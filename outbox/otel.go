package outbox

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// 追踪：每次发布一个 span，并把 W3C traceparent 注入事件 metadata，
// 让消费端可以续接链路（例如在 task handler 里 Extract 后开子 span）。
//
// 使用全局 TracerProvider / Propagator（otelx 安装）；未启用时是 noop。
var relayTracer = otel.Tracer("github.com/fitan/fxkit/outbox")

// startPublishSpan 为一次发布创建 span。
func startPublishSpan(ctx context.Context, topic string) (context.Context, trace.Span) {
	return relayTracer.Start(ctx, "outbox.publish",
		trace.WithAttributes(attribute.String("outbox.topic", topic)))
}

// injectTraceContext 把当前 span 的 traceparent 写入 metadata（MapCarrier）。
func injectTraceContext(ctx context.Context, carrier map[string]string) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(carrier))
}

// ExtractTrace 从事件 metadata 恢复发布方的 trace context，供消费端续接链路。
//
// 消费端在 handler 开头调用一次即可：
//
//	ctx = outbox.ExtractTrace(ctx, metadata)
//	defer outbox.Inbox{}.Once(ctx, ...) // 或者直接开业务 span
//
// metadata 里没有 traceparent 时原样返回 ctx（noop propagator 同理）。
func ExtractTrace(ctx context.Context, metadata map[string]string) context.Context {
	if len(metadata) == 0 {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(metadata))
}
