package hatchetx

import (
	"context"
	"log/slog"
	"sync"

	"github.com/hatchet-dev/hatchet/pkg/worker"
	hatchetotel "github.com/hatchet-dev/hatchet/sdks/go/opentelemetry"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// middlewareHost 由 *hatchet.Worker 满足。
type middlewareHost interface {
	Use(mws ...worker.MiddlewareFunc)
}

var (
	spanProcessorMu           sync.Mutex
	registeredTracerProviders = make(map[*sdktrace.TracerProvider]struct{})
)

func registerHatchetSpanProcessor(tp *sdktrace.TracerProvider) {
	spanProcessorMu.Lock()
	defer spanProcessorMu.Unlock()
	if _, ok := registeredTracerProviders[tp]; ok {
		return
	}
	tp.RegisterSpanProcessor(hatchetotel.NewHatchetAttributeSpanProcessor(passthroughSpanProcessor{}))
	registeredTracerProviders[tp] = struct{}{}
}

// attachWorkerTracing 在 worker 上注册 Hatchet 的消费者 span 中间件。
//
// 生产者 span（hatchet.run_workflow）已由 SDK Run() 基于全局 TracerProvider
// 启动。本钩子在 worker 上补充 hatchet.start_step_run。
//
// 复用 otelx 的 SDK provider，因此不会用第二个 SDK 调用 otel.SetTracerProvider，
// hatchetx 也从不 Shutdown 这个共享 provider。
// span 只发往 otelx（DisableHatchetCollector）：engine 的 OTLP 路径
// 不会把 catalog trace 与 hatchet.run/* 的 SERVER_OTEL span 关联起来。
func attachWorkerTracing(w middlewareHost, enabled bool) bool {
	if !enabled || w == nil {
		return false
	}
	tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	if !ok {
		slog.Info("hatchetx: worker traces skipped (otelx TracerProvider not installed)")
		return false
	}
	inst, err := hatchetotel.NewInstrumentor(
		hatchetotel.WithTracerProvider(tp),
		hatchetotel.DisableHatchetCollector(),
	)
	if err != nil {
		slog.Warn("hatchetx: worker traces disabled", "error", err)
		return false
	}
	// Collector 已关闭，NewInstrumentor 不会注册属性
	// 处理器。挂一个透传包装，让子 span 仍能带上 hatchet.*。
	registerHatchetSpanProcessor(tp)
	w.Use(inst.Middleware())
	slog.Info("hatchetx: worker traces enabled")
	return true
}

// passthroughSpanProcessor 让 HatchetAttributeSpanProcessor 注入属性
// 而不持有 exporter。otelx 已通过自己的处理器导出。
type passthroughSpanProcessor struct{}

func (passthroughSpanProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (passthroughSpanProcessor) OnEnd(sdktrace.ReadOnlySpan)                     {}
func (passthroughSpanProcessor) Shutdown(context.Context) error                  { return nil }
func (passthroughSpanProcessor) ForceFlush(context.Context) error                { return nil }
