package otelx

import (
	"context"
	"log/slog"

	oteltrace "go.opentelemetry.io/otel/trace"
)

// traceHandler 包装 slog.Handler，从活跃 OTel span 向每条记录注入 trace_id/span_id。
// 作为轻量预过滤，使下游 handler 无需感知 OTel。
type traceHandler struct {
	// inner 是被包装的下游 handler，负责实际输出。
	inner slog.Handler
}

// Enabled 委托内层 handler 判断该级别是否启用。
func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle 从 ctx 的活跃 span 注入 trace_id/span_id 后转交内层 handler。
func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := oteltrace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs 返回携带属性的新 traceHandler，内层 handler 同步派生。
func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup 返回使用指定 group 的新 traceHandler，内层 handler 同步派生。
func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{inner: h.inner.WithGroup(name)}
}

// fanoutHandler 将每条记录分发给多个下游 handler，
// 使单个 slog.Default 可同时写入控制台与 OTel log 管道。
type fanoutHandler struct {
	// handlers 是按顺序接收每条记录的下游 handler。
	handlers []slog.Handler
}

// Enabled 在任一内层 handler 启用该级别时返回 true。
func (h *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle 向每个启用的内层 handler 分发记录副本，返回首个错误。
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

// WithAttrs 返回各内层 handler 都应用了属性的新 fanoutHandler。
func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		cloned[i] = handler.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: cloned}
}

// WithGroup 返回各内层 handler 都进入指定 group 的新 fanoutHandler。
func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	cloned := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		cloned[i] = handler.WithGroup(name)
	}
	return &fanoutHandler{handlers: cloned}
}
