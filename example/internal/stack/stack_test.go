package stack_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/fitan/fxkit-example/internal/stack"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getFreePort 获取一个本机可用的随机端口
func getFreePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestStack_WithMergedDocs_Wiring(t *testing.T) {
	port := strconv.Itoa(getFreePort(t))

	// 构造测试环境配置文件
	cfgYAML := `
app:
  name: "stack-test-app"
server:
  port: "` + port + `"
db:
  driver: ""
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(tmpFile, []byte(cfgYAML), 0o600))

	baseYAML := []byte(`
openapi: 3.1.0
info:
  title: "Test Base OpenAPI"
  version: "1.0.0"
`)

	root := di.New(
		func(i di.Injector) {
			di.ProvideValue(i, config.Options{ConfigFile: tmpFile})
		},
		stack.WithMergedDocs(baseYAML),
	)
	defer root.Shutdown()

	// 1. 验证基础服务是否能正常解析
	appCfg, err := di.Invoke[*config.App](root)
	require.NoError(t, err)
	assert.Equal(t, "stack-test-app", appCfg.Name)

	srvCfg, err := di.Invoke[*server.Config](root)
	require.NoError(t, err)
	assert.Equal(t, port, srvCfg.Port)

	// 2. 验证生命周期管理
	lc, err := di.Invoke[*di.Lifecycle](root)
	require.NoError(t, err)
	require.NotNil(t, lc)

	// 启动生命周期并验证能够正常关闭
	ctx := context.Background()
	require.NoError(t, lc.Start(ctx))
	require.NoError(t, lc.Stop(ctx))
}
