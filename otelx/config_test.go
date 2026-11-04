package otelx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestParseSampler 覆盖所有受支持的采样器写法；
// 非法配置必须在启动期报错，而不是静默降级为「全采样」。
func TestParseSampler(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
		desc    string
	}{
		{"", false, "empty defaults to always_on"},
		{"always_on", false, "always sample"},
		{"always_off", false, "never sample"},
		{"trace_id_ratio:0", false, "zero ratio"},
		{"trace_id_ratio:0.5", false, "half ratio"},
		{"trace_id_ratio:1", false, "full ratio"},
		{"parent_based_always_on", false, "parent based"},
		{"parent_based_always_off", false, "parent based off"},
		{"parent_based_trace_id_ratio:0.25", false, "parent based ratio"},
		{"trace_id_ratio:1.5", true, "ratio > 1 must fail"},
		{"trace_id_ratio:-0.1", true, "negative ratio must fail"},
		{"trace_id_ratio:abc", true, "non-numeric ratio must fail"},
		{"trace_id_ratio", true, "missing value must fail"},
		{"unknown_sampler", true, "unknown name must fail"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			s, err := parseSampler(tc.in)
			if tc.wantErr {
				assert.Error(t, err, "input %q should be rejected", tc.in)
				return
			}
			require.NoError(t, err, "input %q should be accepted", tc.in)
			assert.NotNil(t, s)
		})
	}
}

// TestParseSamplerReturnsWorkingSampler 确认返回的 Sampler 真的可用。
func TestParseSamplerReturnsWorkingSampler(t *testing.T) {
	s, err := parseSampler("always_on")
	require.NoError(t, err)
	assert.Equal(t, sdktrace.RecordAndSample, s.ShouldSample(sdktrace.SamplingParameters{}).Decision)
}

// TestParseRatio 单独覆盖比例解析边界。
func TestParseRatio(t *testing.T) {
	r, err := parseRatio("trace_id_ratio:0.3")
	require.NoError(t, err)
	assert.InDelta(t, 0.3, r, 1e-9)

	_, err = parseRatio("nocolon")
	assert.Error(t, err)

	_, err = parseRatio("a:b:c")
	assert.Error(t, err, "non-numeric value must fail")
}

// TestConfig_SetDefaults 默认值决定「未配置时是否上报」，必须稳定。
func TestConfig_SetDefaults(t *testing.T) {
	var c Config
	c.SetDefaults()

	assert.NotEmpty(t, c.Sampling, "sampling must have a default")
	assert.NotEmpty(t, c.Endpoint, "endpoint must have a default")
	assert.NotEmpty(t, c.Protocol, "protocol must have a default")
	assert.NotEmpty(t, c.Traces.Exporter)
	assert.NotEmpty(t, c.Metrics.Exporter)
	assert.NotEmpty(t, c.Logs.Exporter)
}

// TestConfig_Validate 非法配置必须在启动期失败。
func TestConfig_Validate(t *testing.T) {
	valid := func() Config {
		var c Config
		c.SetDefaults()
		c.Endpoint = "localhost:4317"
		c.Protocol = "grpc"
		c.Sampling = "always_on"
		c.Traces.Exporter = "otlp"
		c.Metrics.Exporter = "otlp"
		c.Logs.Exporter = "otlp"
		return c
	}

	t.Run("defaults are valid", func(t *testing.T) {
		assert.NoError(t, valid().Validate())
	})

	t.Run("bad sampler rejected", func(t *testing.T) {
		c := valid()
		c.Sampling = "nonsense"
		assert.Error(t, c.Validate())
	})

	t.Run("bad exporter rejected", func(t *testing.T) {
		c := valid()
		c.Traces.Exporter = "kafka"
		assert.Error(t, c.Validate())
	})

	t.Run("bad protocol rejected", func(t *testing.T) {
		c := valid()
		c.Protocol = "carrier-pigeon"
		assert.Error(t, c.Validate())
	})
}

// TestSetup_DisabledIsNoop 未启用时 Setup 必须是安全的空操作。
func TestSetup_DisabledIsNoop(t *testing.T) {
	var c Config
	c.SetDefaults()
	c.Enabled = false

	assert.NotPanics(t, func() {
		assert.NoError(t, Setup(nil, &c, nil))
	})
}

// TestSetup_NilConfigIsNoop nil 配置不应 panic。
func TestSetup_NilConfigIsNoop(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.NoError(t, Setup(nil, nil, nil))
	})
}

// TestSetup_EnabledWithoutServiceName 启用但既没配 service_name、
// 又没有 app.name 时必须在启动期报错（否则上报的 service 名是空的）。
func TestSetup_EnabledWithoutServiceName(t *testing.T) {
	var c Config
	c.SetDefaults()
	c.Enabled = true
	c.ServiceName = ""
	c.Endpoint = "localhost:4317"
	c.Protocol = "grpc"

	err := Setup(nil, &c, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service_name")
}
