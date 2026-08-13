// Package otelx 引导 OpenTelemetry SDK 的 trace、metrics 与 log 管道，
// 并安装同时输出到 stdout 与 OTLP log exporter 的 slog handler，从活跃 span 注入 trace_id/span_id。
//
// 默认关闭；在 config.yaml 中设置 otel.enabled=true 启用。
// 遵循 [github.com/fitan/fxkit/config.OtelConfig] 的 sampling、exporter 与 runtime_metrics 字段。
package otelx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/logx"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.uber.org/fx"
)

// Setup 是安装 OTel SDK 的 Fx invoker。由 [Module] 装配；仅测试时直接调用。
func Setup(lc fx.Lifecycle, cfg *config.Config) error {
	logx.SetupDefault()

	c := cfg.Get()
	if !c.Otel.Enabled {
		slog.Info("otel disabled")
		return nil
	}

	ctx := context.Background()

	res, err := newResource(c.Otel)
	if err != nil {
		return err
	}

	var shutdowns []func(context.Context) error

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if c.Otel.Traces.Enabled {
		tp, err := newTracerProvider(ctx, c.Otel, res)
		if err != nil {
			return err
		}
		otel.SetTracerProvider(tp)
		shutdowns = append(shutdowns, tp.Shutdown)
	}

	if c.Otel.Metrics.Enabled {
		mp, err := newMeterProvider(ctx, c.Otel, res)
		if err != nil {
			return err
		}
		otel.SetMeterProvider(mp)
		shutdowns = append(shutdowns, mp.Shutdown)

		if c.Otel.Metrics.RuntimeMetrics {
			// Instruments register on the global MeterProvider; mp.Shutdown above tears them down.
			if err := startRuntimeMetrics(); err != nil {
				return err
			}
		}
	}

	if c.Otel.Logs.Enabled {
		lp, err := newLoggerProvider(ctx, c.Otel, res)
		if err != nil {
			return err
		}
		shutdowns = append(shutdowns, lp.Shutdown)

		otelHandler := otelslog.NewHandler("", otelslog.WithLoggerProvider(lp))
		consoleHandler := logx.NewConsoleHandler(os.Stderr, nil)
		slog.SetDefault(slog.New(&traceHandler{
			inner: &fanoutHandler{
				handlers: []slog.Handler{consoleHandler, otelHandler},
			},
		}))
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			slog.Info("otel shutting down")
			var errs []error
			for _, fn := range shutdowns {
				if err := fn(ctx); err != nil {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		},
	})

	slog.Info("otel initialised",
		"service", c.Otel.ServiceName,
		"endpoint", c.Otel.Endpoint,
		"protocol", c.Otel.Protocol,
		"sampling", c.Otel.Sampling,
		"traces", fmt.Sprintf("%v/%s", c.Otel.Traces.Enabled, c.Otel.Traces.Exporter),
		"metrics", fmt.Sprintf("%v/%s", c.Otel.Metrics.Enabled, c.Otel.Metrics.Exporter),
		"logs", fmt.Sprintf("%v/%s", c.Otel.Logs.Enabled, c.Otel.Logs.Exporter),
	)

	return nil
}

func newResource(c config.OtelConfig) (*resource.Resource, error) {
	return resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(c.ServiceName),
			semconv.ServiceVersion(c.ServiceVersion),
			attribute.String("deployment.environment", c.Environment),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
	)
}

func newTracerProvider(ctx context.Context, c config.OtelConfig, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	sampler, err := parseSampler(c.Sampling)
	if err != nil {
		return nil, err
	}

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	}

	switch c.Traces.Exporter {
	case "stdout":
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, err
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(exp))
	default:
		exp, err := newTraceExporter(ctx, c)
		if err != nil {
			return nil, err
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(exp))
	}

	return sdktrace.NewTracerProvider(tpOpts...), nil
}

func newMeterProvider(ctx context.Context, c config.OtelConfig, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	mpOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}

	switch c.Metrics.Exporter {
	case "stdout":
		exp, err := stdoutmetric.New()
		if err != nil {
			return nil, err
		}
		mpOpts = append(mpOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)))
	default:
		exp, err := newMetricExporter(ctx, c)
		if err != nil {
			return nil, err
		}
		mpOpts = append(mpOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)))
	}

	return sdkmetric.NewMeterProvider(mpOpts...), nil
}

func newLoggerProvider(ctx context.Context, c config.OtelConfig, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	lpOpts := []sdklog.LoggerProviderOption{sdklog.WithResource(res)}

	switch c.Logs.Exporter {
	case "stdout":
		exp, err := stdoutlog.New()
		if err != nil {
			return nil, err
		}
		lpOpts = append(lpOpts, sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
	default:
		exp, err := newLogExporter(ctx, c)
		if err != nil {
			return nil, err
		}
		lpOpts = append(lpOpts, sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
	}

	return sdklog.NewLoggerProvider(lpOpts...), nil
}

func otlpUsesHTTP(c config.OtelConfig, exporter string) bool {
	switch exporter {
	case "otlp_http", "http":
		return true
	case "otlp", "grpc", "":
		return strings.EqualFold(c.Protocol, "http")
	default:
		return strings.EqualFold(c.Protocol, "http")
	}
}

func newTraceExporter(ctx context.Context, c config.OtelConfig) (sdktrace.SpanExporter, error) {
	ep := normalizeOTLPEndpoint(c.Endpoint)
	if otlpUsesHTTP(c, c.Traces.Exporter) {
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(ep)}
		if c.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, opts...)
	}
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(ep)}
	if c.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	return otlptracegrpc.New(ctx, opts...)
}

func newMetricExporter(ctx context.Context, c config.OtelConfig) (sdkmetric.Exporter, error) {
	ep := normalizeOTLPEndpoint(c.Endpoint)
	if otlpUsesHTTP(c, c.Metrics.Exporter) {
		opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(ep)}
		if c.Insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		return otlpmetrichttp.New(ctx, opts...)
	}
	opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(ep)}
	if c.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	return otlpmetricgrpc.New(ctx, opts...)
}

func newLogExporter(ctx context.Context, c config.OtelConfig) (sdklog.Exporter, error) {
	ep := normalizeOTLPEndpoint(c.Endpoint)
	if otlpUsesHTTP(c, c.Logs.Exporter) {
		opts := []otlploghttp.Option{otlploghttp.WithEndpoint(ep)}
		if c.Insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
		return otlploghttp.New(ctx, opts...)
	}
	opts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(ep)}
	if c.Insecure {
		opts = append(opts, otlploggrpc.WithInsecure())
	}
	return otlploggrpc.New(ctx, opts...)
}

// normalizeOTLPEndpoint strips http(s):// so WithEndpoint receives host:port.
func normalizeOTLPEndpoint(endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	ep = strings.TrimPrefix(ep, "https://")
	ep = strings.TrimPrefix(ep, "http://")
	return strings.TrimRight(ep, "/")
}

// parseSampler 将字符串规格转换为 [sdktrace.Sampler]：
//
//	always_on
//	always_off
//	trace_id_ratio:0.5
//	parent_based_always_on
//	parent_based_always_off
//	parent_based_trace_id_ratio:0.5
func parseSampler(s string) (sdktrace.Sampler, error) {
	switch {
	case s == "" || s == "always_on":
		return sdktrace.AlwaysSample(), nil
	case s == "always_off":
		return sdktrace.NeverSample(), nil
	case strings.HasPrefix(s, "trace_id_ratio:"):
		ratio, err := parseRatio(s)
		if err != nil {
			return nil, err
		}
		return sdktrace.TraceIDRatioBased(ratio), nil
	case s == "parent_based_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample()), nil
	case s == "parent_based_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample()), nil
	case strings.HasPrefix(s, "parent_based_trace_id_ratio:"):
		ratio, err := parseRatio(s)
		if err != nil {
			return nil, err
		}
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio)), nil
	default:
		return nil, fmt.Errorf("unknown sampler %q", s)
	}
}

func parseRatio(s string) (float64, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid ratio format %q, expected key:value", s)
	}
	ratio, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid ratio %q: %w", parts[1], err)
	}
	if ratio < 0 || ratio > 1 {
		return 0, fmt.Errorf("ratio %v out of range [0,1]", ratio)
	}
	return ratio, nil
}

// Module 在 otel.enabled 为 true 时安装 OTel SDK 生命周期。
var Module = fx.Module("fxkit/otelx",
	fx.Invoke(Setup),
)
