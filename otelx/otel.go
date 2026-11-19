// Package otelx 引导 OpenTelemetry SDK 的 trace、metrics 与 log 管道，
// 并安装同时输出到 stdout 与 OTLP log exporter 的 slog handler，从活跃 span 注入 trace_id/span_id。
//
// 默认关闭；在 config.yaml 中设置 otel.enabled=true 启用。
// 遵循 [Config] 的 sampling、exporter 与 runtime_metrics 字段。
package otelx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
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
)

// Setup 是安装 OTel SDK 的 Fx invoker。由 [Module] 装配；仅测试时直接调用。
func Setup(lc *di.Lifecycle, otelCfg *Config, app *config.App) error {
	logx.SetupDefault()

	if otelCfg == nil || !otelCfg.Enabled {
		slog.Info("otel disabled")
		return nil
	}

	cfg := *otelCfg
	if strings.TrimSpace(cfg.ServiceName) == "" {
		if app == nil || strings.TrimSpace(app.Name) == "" {
			return fmt.Errorf("otel.service_name is empty and app.name is empty")
		}
		cfg.ServiceName = strings.TrimSpace(app.Name)
	}
	if strings.TrimSpace(cfg.ServiceVersion) == "" {
		// 与 service_name 回退 app.name 一致：未显式配置时用 -ldflags 注入的构建版本
		// （未注入时为 "dev"），避免资源上完全没有 service.version。
		cfg.ServiceVersion = strings.TrimSpace(buildinfo.Version)
	}

	ctx := context.Background()

	res, err := newResource(cfg)
	if err != nil {
		return err
	}

	var shutdowns []func(context.Context) error

	// 若 setup 中途失败，已安装的 provider 必须回滚，否则 batcher goroutine
	// 常驻且退出时 span/log 全部丢失。
	success := false
	defer func() {
		if success {
			return
		}
		ctx := context.Background()
		for i := len(shutdowns) - 1; i >= 0; i-- {
			_ = shutdowns[i](ctx)
		}
	}()

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if cfg.Traces.Enabled {
		tp, err := newTracerProvider(ctx, cfg, res)
		if err != nil {
			return err
		}
		otel.SetTracerProvider(tp)
		shutdowns = append(shutdowns, tp.Shutdown)
	}

	if cfg.Metrics.Enabled {
		mp, err := newMeterProvider(ctx, cfg, res)
		if err != nil {
			return err
		}
		otel.SetMeterProvider(mp)
		shutdowns = append(shutdowns, mp.Shutdown)

		if cfg.Metrics.RuntimeMetrics {
			// Instrument 会注册到全局 MeterProvider；上面的 mp.Shutdown 会把它们拆除。
			if err := startRuntimeMetrics(); err != nil {
				return err
			}
		}
	}

	consoleHandler := logx.NewConsoleHandler(os.Stderr, nil)
	var rootHandler slog.Handler = consoleHandler

	if cfg.Logs.Enabled {
		lp, err := newLoggerProvider(ctx, cfg, res)
		if err != nil {
			return err
		}
		shutdowns = append(shutdowns, lp.Shutdown)

		otelHandler := otelslog.NewHandler("", otelslog.WithLoggerProvider(lp))
		rootHandler = &fanoutHandler{
			handlers: []slog.Handler{consoleHandler, otelHandler},
		}
	}

	// 库嵌入场景可以用 logs.replace_slog_default=false 保留宿主 handler。
	replaceDefault := true
	if cfg.Logs.ReplaceSlogDefault != nil {
		replaceDefault = *cfg.Logs.ReplaceSlogDefault
	}
	if replaceDefault {
		slog.SetDefault(slog.New(&traceHandler{
			inner: rootHandler,
		}))
	}

	if lc != nil {
		lc.Append(di.Hook{
			OnStop: func(ctx context.Context) error {
				slog.Info("otel shutting down")

				var errs []error
				// LIFO：先关 logs（最先产生），最后关 traces。
				// 每个 exporter 独立预算：共享一个 5s 时，日志 flush 慢会把
				// metrics/traces 的 ctx 提前耗尽，恰恰丢掉最需要的链路数据。
				for i := len(shutdowns) - 1; i >= 0; i-- {
					flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					err := shutdowns[i](flushCtx)
					cancel()
					if err != nil {
						errs = append(errs, err)
					}
				}
				return errors.Join(errs...)
			},
		})
	}

	slog.Info("otel initialised",
		"service", cfg.ServiceName,
		"endpoint", cfg.Endpoint,
		"protocol", cfg.Protocol,
		"sampling", cfg.Sampling,
		"traces", fmt.Sprintf("%v/%s", cfg.Traces.Enabled, cfg.Traces.Exporter),
		"metrics", fmt.Sprintf("%v/%s", cfg.Metrics.Enabled, cfg.Metrics.Exporter),
		"logs", fmt.Sprintf("%v/%s", cfg.Logs.Enabled, cfg.Logs.Exporter),
	)

	success = true
	return nil
}

// newResource 构造带 service 名、版本、环境与主机信息的 OTel resource。
func newResource(c Config) (*resource.Resource, error) {
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

// newTracerProvider 按 sampling 与 traces.exporter 构造带 batch 导出的 TracerProvider。
func newTracerProvider(ctx context.Context, c Config, res *resource.Resource) (*sdktrace.TracerProvider, error) {
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

// newMeterProvider 按 metrics.exporter 构造带周期性 reader 的 MeterProvider。
func newMeterProvider(ctx context.Context, c Config, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
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

// newLoggerProvider 按 logs.exporter 构造带 batch processor 的 LoggerProvider。
func newLoggerProvider(ctx context.Context, c Config, res *resource.Resource) (*sdklog.LoggerProvider, error) {
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

// otlpUsesHTTP 报告 OTLP 导出是否走 HTTP（否则走 gRPC）。
//
// 由 protocol 字段单独决定：早期接受 otlp_http/http/grpc/otlp_grpc 等 6 个
// exporter 别名，且 `grpc` 会被 `protocol: http` 覆盖——配置看起来生效、实际不生效。
func otlpUsesHTTP(c Config) bool {
	return strings.EqualFold(c.Protocol, "http")
}

// newTraceExporter 按 protocol 构造 OTLP HTTP 或 gRPC 的 trace exporter。
func newTraceExporter(ctx context.Context, c Config) (sdktrace.SpanExporter, error) {
	ep := normalizeOTLPEndpoint(c.Endpoint)
	if otlpUsesHTTP(c) {
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

// newMetricExporter 按 protocol 构造 OTLP HTTP 或 gRPC 的 metric exporter。
func newMetricExporter(ctx context.Context, c Config) (sdkmetric.Exporter, error) {
	ep := normalizeOTLPEndpoint(c.Endpoint)
	if otlpUsesHTTP(c) {
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

// newLogExporter 按 protocol 构造 OTLP HTTP 或 gRPC 的 log exporter。
func newLogExporter(ctx context.Context, c Config) (sdklog.Exporter, error) {
	ep := normalizeOTLPEndpoint(c.Endpoint)
	if otlpUsesHTTP(c) {
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

// normalizeOTLPEndpoint 去除 http(s)://，以便 WithEndpoint 接收 host:port。
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

// parseRatio 解析 "key:ratio" 形式的字符串，ratio 须在 [0,1] 内。
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
//
// 安装动作放在最早的 PreStart 阶段：配置错误（如 service_name 缺失、exporter
// 构造失败）会中止启动，而不是被静默吞掉、服务正常起来但遥测完全缺失。
var Module = di.Module("fxkit/otelx",
	config.Provide[Config]("otel"),
	func(i di.Injector) {
		lc := di.MustInvoke[*di.Lifecycle](i)
		if lc == nil {
			cfg, err := di.Invoke[*Config](i)
			if err != nil {
				panic(fmt.Sprintf("otelx: %v", err))
			}
			app, _ := di.Invoke[*config.App](i)
			if err := Setup(nil, cfg, app); err != nil {
				panic(fmt.Sprintf("otelx: %v", err))
			}
			return
		}

		lc.AppendPreStartPhase(di.PhaseResolveContributors-10, di.Hook{
			OnStart: func(context.Context) error {
				cfg, err := di.Invoke[*Config](i)
				if err != nil {
					return fmt.Errorf("otelx: %w", err)
				}
				app, _ := di.Invoke[*config.App](i)
				return Setup(lc, cfg, app)
			},
		})
	},
)
