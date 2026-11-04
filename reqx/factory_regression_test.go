package reqx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFactory_ClientAfterClose 是回归测试：
// Close() 会取消内部 root context，之后创建的 Consul watch 会立即退出，
// 端点池永远为空 —— 业务表现为「服务一直连不上」，极难排查。
// 现在显式返回错误。
func TestFactory_ClientAfterClose(t *testing.T) {
	f := NewFactory(nil, nil)
	f.Close()

	assert.True(t, f.IsClosed())

	_, err := f.Client(ClientInput{Name: "orders", Seeds: []string{"127.0.0.1:1"}})
	require.Error(t, err, "creating a client after Close must fail instead of silently degrading")
	assert.Contains(t, err.Error(), "closed")
}

// TestFactory_ClientBeforeClose 关闭前正常可用（含静态 seeds，不依赖 Consul）。
func TestFactory_ClientBeforeClose(t *testing.T) {
	f := NewFactory(nil, nil)
	defer f.Close()

	assert.False(t, f.IsClosed())

	cli, err := f.Client(ClientInput{Seeds: []string{"127.0.0.1:9"}})
	require.NoError(t, err)
	assert.NotNil(t, cli)

	assert.Equal(t, []string{"127.0.0.1:9"}, f.Endpoints(ClientInput{Seeds: []string{"127.0.0.1:9"}}))
}

// TestFactory_CloseIsIdempotent 重复 Close 不应 panic。
func TestFactory_CloseIsIdempotent(t *testing.T) {
	f := NewFactory(nil, nil)
	assert.NotPanics(t, func() {
		f.Close()
		f.Close()
	})
}

// TestFactory_NilSafe nil Factory 的查询方法不应 panic。
func TestFactory_NilSafe(t *testing.T) {
	var f *Factory
	assert.Nil(t, f.Endpoints(ClientInput{Seeds: []string{"a:1"}}))
	assert.True(t, f.IsClosed())

	_, err := f.Client(ClientInput{Seeds: []string{"a:1"}})
	require.Error(t, err)
}

// TestFactory_RequiresNameOrSeeds 空输入必须报错，避免建出一个没有目标地址的 client。
func TestFactory_RequiresNameOrSeeds(t *testing.T) {
	f := NewFactory(nil, nil)
	defer f.Close()

	_, err := f.Client(ClientInput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Name or Seeds required")
}

// TestFactory_RejectsBadScheme scheme 只允许 http/https。
func TestFactory_RejectsBadScheme(t *testing.T) {
	f := NewFactory(nil, nil)
	defer f.Close()

	_, err := f.Client(ClientInput{Seeds: []string{"a:1"}, Scheme: "ftp"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported scheme")
}
