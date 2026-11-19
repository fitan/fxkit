package outbox

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TestPublishMetadataInjectsTraceparent 验证 outbox → 消费端的 trace 上下文传播。
func TestPublishMetadataInjectsTraceparent(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(prev)

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	key := "k1"
	meta := publishMetadata(ctx, OutboxEvent{IdempotencyKey: &key})
	if meta == nil {
		t.Fatal("metadata should not be nil")
	}
	if meta[MetadataIdempotencyKey] != "k1" {
		t.Fatalf("idempotency key missing: %v", meta)
	}
	tp, ok := meta["traceparent"]
	if !ok || tp == "" {
		t.Fatalf("traceparent not injected: %v", meta)
	}
	if want := "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01"; tp != want {
		t.Fatalf("traceparent = %q, want %q", tp, want)
	}
}

// TestPublishMetadataWithoutSpan 没有 span 时不写入 traceparent。
func TestPublishMetadataWithoutSpan(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(prev)

	if meta := publishMetadata(context.Background(), OutboxEvent{}); meta != nil {
		t.Fatalf("expected nil metadata, got %v", meta)
	}
}

// TestExtractTraceRestoresRemoteContext 验证消费端能从 metadata 续接发布方链路。
func TestExtractTraceRestoresRemoteContext(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(prev)

	want := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	meta := map[string]string{}
	otel.GetTextMapPropagator().Inject(trace.ContextWithSpanContext(context.Background(), want), propagation.MapCarrier(meta))

	got := trace.SpanContextFromContext(ExtractTrace(context.Background(), meta))
	if !got.IsValid() {
		t.Fatal("extracted span context is invalid")
	}
	if got.TraceID() != want.TraceID() || got.SpanID() != want.SpanID() {
		t.Fatalf("got %s/%s, want %s/%s", got.TraceID(), got.SpanID(), want.TraceID(), want.SpanID())
	}
}

// TestExtractTraceWithoutMetadata 空 metadata 原样返回 ctx。
func TestExtractTraceWithoutMetadata(t *testing.T) {
	ctx := context.Background()
	if got := ExtractTrace(ctx, nil); got != ctx {
		t.Fatal("expected original context when metadata is empty")
	}
}
