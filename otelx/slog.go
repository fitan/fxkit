package otelx

import (
	"context"
	"log/slog"

	oteltrace "go.opentelemetry.io/otel/trace"
)

// traceHandler 包装 slog.Handler，从活跃 OTel span 向每条记录注入 trace_id/span_id。
// 作为轻量预过滤，使下游 handler 无需感知 OTel。
type traceHandler struct {
	inner slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := oteltrace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{inner: h.inner.WithGroup(name)}
}

// fanoutHandler 将每条记录分发给多个下游 handler，
// 使单个 slog.Default 可同时写入控制台与 OTel log 管道。
type fanoutHandler struct {
	handlers []slog.Handler
}

func (h *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, r.Level) {
			if err := handler.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		cloned[i] = handler.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: cloned}
}

func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	cloned := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		cloned[i] = handler.WithGroup(name)
	}
	return &fanoutHandler{handlers: cloned}
}
