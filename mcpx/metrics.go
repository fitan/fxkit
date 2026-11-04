package mcpx

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// mcpTracer 用于 mcpx.tool span。
var mcpTracer = otel.Tracer("github.com/fitan/fxkit/mcpx")

// 业务指标：tool 调用次数 / 耗时 / 结果。
//
// tool 名是有限集合（来自 OpenAPI operation），作为 attribute 不会造成高基数。
var (
	mcpMetricsOnce sync.Once
	toolCalls      metric.Int64Counter
	toolDuration   metric.Float64Histogram
)

func initMCPMetrics() {
	mcpMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/mcpx")
		toolCalls, _ = m.Int64Counter(
			"mcpx.tool.calls",
			metric.WithDescription("MCP tool invocations"),
		)
		toolDuration, _ = m.Float64Histogram(
			"mcpx.tool.duration",
			metric.WithUnit("s"),
			metric.WithDescription("MCP tool invocation duration"),
		)
	})
}

func recordToolCall(ctx context.Context, tool string, d time.Duration, isErr bool) {
	initMCPMetrics()
	result := "ok"
	if isErr {
		result = "error"
	}
	attrs := metric.WithAttributes(
		attribute.String("mcp.tool", tool),
		attribute.String("mcp.result", result),
	)
	if toolCalls != nil {
		toolCalls.Add(ctx, 1, attrs)
	}
	if toolDuration != nil {
		toolDuration.Record(ctx, d.Seconds(), attrs)
	}
}

// startToolSpan 为一次 tool 调用创建 span。
func startToolSpan(ctx context.Context, tool Tool) (context.Context, trace.Span) {
	return mcpTracer.Start(ctx, "mcpx.tool", trace.WithAttributes(
		attribute.String("mcp.tool", tool.Name),
		attribute.String("mcp.method", tool.Method),
	))
}

func endToolSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "tool call failed")
	}
	span.End()
}
