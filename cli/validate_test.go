package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeSection 是 validate 测试用的配置段。
type probeSection struct {
	Name string `yaml:"name"`
	Size int    `yaml:"size"`
}

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// runValidate 以给定配置执行 validate 命令，返回输出与错误。
//
// --config 定义在根命令上（PersistentFlags），所以这里构造一个最小根命令来挂载它。
func runValidate(t *testing.T, cfgPath string, strict bool, extra ...di.Option) (string, error) {
	t.Helper()

	root := &cobra.Command{Use: "app"}
	root.PersistentFlags().StringP("config", "c", "", "config file")
	root.PersistentFlags().String("consul", "", "consul addr")
	root.PersistentFlags().String("consul-key", "", "consul key")

	sub := buildValidateCmd(extra)
	root.AddCommand(sub)
	args := []string{"validate", "--config", cfgPath}
	if strict {
		args = append(args, "--strict")
	}
	root.SetArgs(args)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	err := root.Execute()
	return out.String(), err
}

// TestValidate_CleanConfig 无死键时通过。
func TestValidate_CleanConfig(t *testing.T) {
	path := writeYAML(t, "probe:\n  name: x\n  size: 1\n")

	out, err := runValidate(t, path, true,
		config.Module,
		config.Provide[probeSection]("probe"),
	)
	require.NoError(t, err)
	assert.Contains(t, out, "config ok: no unused keys")
}

// TestValidate_ReportsUnusedKey 拼写错误/未知键应被报告。
func TestValidate_ReportsUnusedKey(t *testing.T) {
	path := writeYAML(t, "probe:\n  name: x\n  szie: 1\n")

	out, err := runValidate(t, path, false,
		config.Module,
		config.Provide[probeSection]("probe"),
	)
	require.NoError(t, err, "without --strict unused keys are warnings, not errors")
	assert.Contains(t, out, "probe.szie")
}

// TestValidate_StrictFailsOnUnusedKey --strict 时以非零退出。
func TestValidate_StrictFailsOnUnusedKey(t *testing.T) {
	path := writeYAML(t, "probe:\n  name: x\n  szie: 1\n")

	_, err := runValidate(t, path, true,
		config.Module,
		config.Provide[probeSection]("probe"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unused config key")
}

// TestValidate_DoesNotRequireInfra 是核心约束：
// validate 必须能在没有任何基础设施（DB / Redis / Consul）的环境里运行，
// 否则 CI 无法用它做配置门禁。
func TestValidate_DoesNotRequireInfra(t *testing.T) {
	path := writeYAML(t, "probe:\n  name: x\n  size: 1\n")

	done := make(chan error, 1)
	go func() {
		_, err := runValidate(t, path, false,
			config.Module,
			config.Provide[probeSection]("probe"),
		)
		done <- err
	}()

	select {
	case err := <-done:
		// 只要不因「连不上 DB」失败即可
		if err != nil {
			assert.NotContains(t, err.Error(), "gormx")
			assert.NotContains(t, err.Error(), "database")
		}
	case <-timeoutAfter():
		t.Fatal("validate hung — it must not block on external services")
	}
}

// TestValidate_InvalidValueCaught 各段 Validate() 失败要被捕获。
func TestValidate_InvalidValueCaught(t *testing.T) {
	type mustBePositive struct {
		Size int `yaml:"size"`
	}
	// 用 server.Config 的端口校验验证「段级 Validate 真的会跑」
	path := writeYAML(t, "server:\n  port: \"99999\"\n")

	_, err := runValidate(t, path, false, config.Module)
	require.Error(t, err, "server.Config.Validate must reject port 99999")
	assert.Contains(t, err.Error(), "port")
}

// timeoutAfter 是测试用的超时通道。
func timeoutAfter() <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Second)
		close(ch)
	}()
	return ch
}
