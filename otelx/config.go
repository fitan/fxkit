package otelx

import (
	"fmt"
	"strings"
)

// Config OpenTelemetry SDK（yaml: otel）。server 在 HTTPEnabled() 时挂 otelhttp。
type Config struct {
	// Enabled 总开关。默认 false。
	Enabled bool `yaml:"enabled"`
	// ServiceName 资源属性。空则回退 app.name。
	ServiceName string `yaml:"service_name"`
	// ServiceVersion 资源属性；为空时回退 buildinfo.Version（未注入时为 "dev"）。
	ServiceVersion string `yaml:"service_version"`
	// Environment 如 development / production。默认 development。
	Environment string `yaml:"environment"`
	// Endpoint OTLP 地址，如 localhost:4317。可带 http(s)://，导出会去掉 scheme。
	Endpoint string `yaml:"endpoint"`
	// Protocol：grpc | http。默认 grpc。
	Protocol string `yaml:"protocol"`
	// Insecure 明文 gRPC/HTTP。本地默认 true。
	Insecure bool `yaml:"insecure"`
	// Sampling：always_on | always_off | trace_id_ratio:0.5 | parent_based_always_on | parent_based_trace_id_ratio:0.5
	Sampling string `yaml:"sampling"`
	// Traces 控制 trace 管道；默认启用，exporter 默认 otlp。
	Traces TracesConfig `yaml:"traces"`
	// Metrics 控制 metrics 管道；默认启用，exporter 默认 otlp。
	Metrics MetricsConfig `yaml:"metrics"`
	// Logs 控制 log 管道；默认启用，slog 会 fanout 到 OTLP。
	Logs LogsConfig `yaml:"logs"`
}

// TracesConfig 控制 trace 管道。
type TracesConfig struct {
	// Enabled 控制 trace 管道是否启用。
	Enabled bool `yaml:"enabled"` // 默认 true（总开关仍看 otel.enabled）
	// Exporter 选择 trace exporter。
	Exporter string `yaml:"exporter"` // otlp | stdout
}

// MetricsConfig 控制 metrics 管道。
type MetricsConfig struct {
	// Enabled 控制 metrics 管道是否启用。
	Enabled bool `yaml:"enabled"` // 默认 true
	// Exporter 选择 metrics exporter。
	Exporter string `yaml:"exporter"` // otlp | stdout
	// RuntimeMetrics 控制是否注册进程 runtime 指标。
	RuntimeMetrics bool `yaml:"runtime_metrics"` // 进程 runtime 指标，默认 true
}

// LogsConfig 控制 log 管道。
type LogsConfig struct {
	// Enabled 控制 log 管道是否启用。
	Enabled bool `yaml:"enabled"` // 默认 true；slog fanout 到 OTLP
	// Exporter 选择 log exporter。
	Exporter string `yaml:"exporter"` // otlp | stdout
	// ReplaceSlogDefault 是否把 slog.Default 替换为带 trace 注入的 handler。
	// 默认 true。作为库嵌入已有服务时设为 false，避免覆盖宿主已配置的 handler。
	ReplaceSlogDefault *bool `yaml:"replace_slog_default"`
}

// SetDefaults 填入编译期默认值。
func (c *Config) SetDefaults() {
	c.Environment = "development"
	c.Endpoint = "localhost:4317"
	c.Protocol = "grpc"
	c.Insecure = true
	c.Sampling = "always_on"
	c.Traces = TracesConfig{Enabled: true, Exporter: "otlp"}
	c.Metrics = MetricsConfig{Enabled: true, Exporter: "otlp", RuntimeMetrics: true}
	c.Logs = LogsConfig{Enabled: true, Exporter: "otlp", ReplaceSlogDefault: boolPtr(true)}
}

// boolPtr 返回 v 的指针，用于填充 *bool 默认值。
func boolPtr(v bool) *bool { return &v }

// HTTPEnabled 报告 chi 是否应安装 otelhttp（trace 和/或 metrics）。
func (c Config) HTTPEnabled() bool {
	return c.Enabled && (c.Traces.Enabled || c.Metrics.Enabled)
}

// Validate 检查 protocol / sampling / exporter。非法值启动失败。
func (c Config) Validate() error {
	switch strings.ToLower(strings.TrimSpace(c.Protocol)) {
	case "", "grpc", "http":
	default:
		return fmt.Errorf("otel.protocol %q is not grpc or http", c.Protocol)
	}
	if err := validateSampling(c.Sampling); err != nil {
		return err
	}
	if err := validateExporter("otel.traces.exporter", c.Traces.Exporter); err != nil {
		return err
	}
	if err := validateExporter("otel.metrics.exporter", c.Metrics.Exporter); err != nil {
		return err
	}
	if err := validateExporter("otel.logs.exporter", c.Logs.Exporter); err != nil {
		return err
	}
	return nil
}

// validateExporter 校验 exporter 取值是否为 otlp 或 stdout，空值视为默认。
func validateExporter(path, v string) error {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "otlp", "stdout":
		return nil
	default:
		return fmt.Errorf("%s %q is not otlp or stdout (use protocol: http|grpc to pick the OTLP transport)", path, v)
	}
}

// validateSampling 复用 [parseSampler]，避免「配置校验」与「实际解析」
// 各维护一套字符串规则（新增策略时一侧漏改就会 validate 通过、启动报错）。
func validateSampling(s string) error {
	_, err := parseSampler(strings.TrimSpace(s))
	return err
}
