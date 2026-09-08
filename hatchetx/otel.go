package hatchetx

import (
	"context"
	"log/slog"

	"github.com/hatchet-dev/hatchet/pkg/worker"
	hatchetotel "github.com/hatchet-dev/hatchet/sdks/go/opentelemetry"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// middlewareHost is satisfied by *hatchet.Worker.
type middlewareHost interface {
	Use(mws ...worker.MiddlewareFunc)
}

// attachWorkerTracing registers Hatchet's consumer-span middleware on the worker.
//
// Producer spans (hatchet.run_workflow) already start from SDK Run() against the
// global TracerProvider. This hook adds hatchet.start_step_run on the worker.
//
// The otelx SDK provider is reused so we do not call otel.SetTracerProvider with
// a second SDK, and we never Shutdown that shared provider from hatchetx.
// Spans go only to otelx (DisableHatchetCollector): the engine OTLP path does
// not join catalog traces with hatchet.run/* SERVER_OTEL spans.
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
	// Collector is off, so NewInstrumentor does not register the attribute
	// processor. Attach a passthrough wrapper so child spans still get hatchet.*.
	tp.RegisterSpanProcessor(hatchetotel.NewHatchetAttributeSpanProcessor(passthroughSpanProcessor{}))
	w.Use(inst.Middleware())
	slog.Info("hatchetx: worker traces enabled")
	return true
}

// passthroughSpanProcessor lets HatchetAttributeSpanProcessor inject attributes
// without owning an exporter. otelx already exports via its own processors.
type passthroughSpanProcessor struct{}

func (passthroughSpanProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (passthroughSpanProcessor) OnEnd(sdktrace.ReadOnlySpan)                     {}
func (passthroughSpanProcessor) Shutdown(context.Context) error                  { return nil }
func (passthroughSpanProcessor) ForceFlush(context.Context) error                { return nil }
