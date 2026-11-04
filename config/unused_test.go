package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unusedProbe struct {
	PageSize int           `yaml:"page_size"`
	Timeout  time.Duration `yaml:"timeout"`
	Inner    unusedInner   `yaml:"inner"`
}

type unusedInner struct {
	Enabled bool `yaml:"enabled"`
}

// TestUnused_DetectsDeadKeys 是配置静默失效的回归测试：
// yaml 里写了键、但没有任何结构体读它时，过去完全无提示。
func TestUnused_DetectsDeadKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	body := `
probe:
  page_size: 20
  timeout: 5s
  typo_key: oops
  inner:
    enabled: true
    unknown_nested: 1
ghost:
  anything: 1
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	cfg, err := config.New(config.Options{ConfigFile: path})
	require.NoError(t, err)

	// 登记消费方（等价于模块调用 config.Load[unusedProbe]("probe")）
	_, err = config.Load[unusedProbe](cfg, "probe")
	require.NoError(t, err)

	unused := cfg.Unused()
	keys := map[string]string{}
	for _, u := range unused {
		keys[u.Key] = u.Reason
	}

	assert.Contains(t, keys, "probe.typo_key", "unknown key under a consumed section must be reported")
	assert.Contains(t, keys, "probe.inner.unknown_nested", "unknown nested key must be reported")
	assert.Contains(t, keys, "ghost", "section with no consumer must be reported")

	assert.NotContains(t, keys, "probe.page_size", "declared key must not be reported")
	assert.NotContains(t, keys, "probe.inner.enabled", "declared nested key must not be reported")
}

// TestUnused_CleanConfigReportsNothing 合法配置不应产生噪声。
func TestUnused_CleanConfigReportsNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(path, []byte("probe:\n  page_size: 10\n  timeout: 1s\n  inner:\n    enabled: false\n"), 0o600))

	cfg, err := config.New(config.Options{ConfigFile: path})
	require.NoError(t, err)
	_, err = config.Load[unusedProbe](cfg, "probe")
	require.NoError(t, err)

	assert.Empty(t, cfg.Unused(), "clean config should produce no warnings")
}
