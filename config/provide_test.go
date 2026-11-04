package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// provideProbe 用于测试 config.Provide 的注入与校验。
type provideProbe struct {
	PageSize int           `yaml:"page_size"`
	Timeout  time.Duration `yaml:"timeout"`
}

func (p *provideProbe) SetDefaults() {
	p.PageSize = 10
	p.Timeout = 5 * time.Second
}

func (p *provideProbe) Validate() error {
	if p.PageSize <= 0 {
		return errProbePageSize
	}
	return nil
}

var errProbePageSize = errProbe{msg: "page_size must be > 0"}

type errProbe struct{ msg string }

func (e errProbe) Error() string { return e.msg }

func newConfigRoot(t *testing.T, yaml string) *di.RootScope {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	return di.New(
		func(i di.Injector) {
			di.ProvideValue(i, config.Options{ConfigFile: path})
		},
		config.Module,
	)
}

// TestProvide_InjectsAndAppliesDefaults 验证 config.Provide 完成
// 「SetDefaults → overlay yaml → Validate」全链路。
func TestProvide_InjectsAndAppliesDefaults(t *testing.T) {
	root := newConfigRoot(t, "probe:\n  page_size: 42\n")
	defer di.Shutdown(root)

	config.Provide[provideProbe]("probe")(root)

	got, err := di.Invoke[*provideProbe](root)
	require.NoError(t, err)

	assert.Equal(t, 42, got.PageSize, "yaml value must override the default")
	assert.Equal(t, 5*time.Second, got.Timeout, "untouched field keeps SetDefaults value")
}

// TestProvide_ReturnsValidationError 段级 Validate 失败必须让 Invoke 失败。
func TestProvide_ReturnsValidationError(t *testing.T) {
	root := newConfigRoot(t, "probe:\n  page_size: -1\n")
	defer di.Shutdown(root)

	config.Provide[provideProbe]("probe")(root)

	_, err := di.Invoke[*provideProbe](root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "page_size must be > 0")
}

// TestProvide_MissingSectionKeepsDefaults key 不存在时保留默认值，不报错。
func TestProvide_MissingSectionKeepsDefaults(t *testing.T) {
	root := newConfigRoot(t, "other:\n  x: 1\n")
	defer di.Shutdown(root)

	config.Provide[provideProbe]("probe")(root)

	got, err := di.Invoke[*provideProbe](root)
	require.NoError(t, err)
	assert.Equal(t, 10, got.PageSize)
	assert.Equal(t, 5*time.Second, got.Timeout)
}

// TestProvide_RegistersConsumerForUnusedCheck Provide 在注册时即登记消费方，
// 因此 config.Unused 不会对已声明段误报（即使从未调用过 Load）。
func TestProvide_RegistersConsumerForUnusedCheck(t *testing.T) {
	root := newConfigRoot(t, "probe:\n  page_size: 5\n")
	defer di.Shutdown(root)

	config.Provide[provideProbe]("probe")(root)

	cfg, err := di.Invoke[*config.Config](root)
	require.NoError(t, err)

	assert.Empty(t, cfg.Unused(), "Provide must register the consumer eagerly, not on first Load")
}

// TestDurationDecodeHook 覆盖各种 Duration 输入形式。
func TestDurationDecodeHook(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want time.Duration
	}{
		{"go duration string", "d:\n  v: 1h30m\n", 90 * time.Minute},
		{"seconds suffix", "d:\n  v: 45s\n", 45 * time.Second},
		{"milliseconds", "d:\n  v: 200ms\n", 200 * time.Millisecond},
		{"bare int means seconds", "d:\n  v: 30\n", 30 * time.Second},
		{"empty string is zero", "d:\n  v: \"\"\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newConfigRoot(t, tc.yaml)
			defer di.Shutdown(root)

			config.Provide[durationProbe]("d")(root)
			got, err := di.Invoke[*durationProbe](root)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.V)
		})
	}
}

type durationProbe struct {
	V time.Duration `yaml:"v"`
}

// TestDurationDecodeHook_Invalid 非法 duration 必须报错而不是静默为 0。
func TestDurationDecodeHook_Invalid(t *testing.T) {
	root := newConfigRoot(t, "d:\n  v: not-a-duration\n")
	defer di.Shutdown(root)

	config.Provide[durationProbe]("d")(root)
	_, err := di.Invoke[*durationProbe](root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid duration")
}
