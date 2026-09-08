package hatchetx

import (
	"context"
	"testing"

	"github.com/hatchet-dev/hatchet/pkg/worker"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type fakeWorker struct {
	n int
}

func (f *fakeWorker) Use(mws ...worker.MiddlewareFunc) {
	f.n += len(mws)
}

func TestAttachWorkerTracing_DisabledOrNil(t *testing.T) {
	if attachWorkerTracing(nil, true) {
		t.Fatal("nil worker")
	}
	var w fakeWorker
	if attachWorkerTracing(&w, false) {
		t.Fatal("disabled")
	}
	if w.n != 0 {
		t.Fatalf("Use called: %d", w.n)
	}
}

func TestAttachWorkerTracing_NoSDKProvider(t *testing.T) {
	orig := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(orig) })
	otel.SetTracerProvider(noop.NewTracerProvider())

	var w fakeWorker
	if attachWorkerTracing(&w, true) {
		t.Fatal("expected skip without SDK provider")
	}
	if w.n != 0 {
		t.Fatalf("Use called: %d", w.n)
	}
}

func TestAttachWorkerTracing_SDKProvider(t *testing.T) {
	orig := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(orig) })

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	otel.SetTracerProvider(tp)

	var w fakeWorker
	if !attachWorkerTracing(&w, true) {
		t.Fatal("expected attach")
	}
	if w.n != 1 {
		t.Fatalf("Use n=%d", w.n)
	}
}

func TestConfigSetDefaults_OTel(t *testing.T) {
	var c Config
	c.SetDefaults()
	if !c.OTel {
		t.Fatal("hatchet.otel default should be true")
	}
}
