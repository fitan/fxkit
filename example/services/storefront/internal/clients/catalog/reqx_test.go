package catalog_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	catalogclient "github.com/fitan/fxkit-example/services/storefront/internal/clients/catalog"
	"github.com/fitan/fxkit/reqx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewFromFactory 验证 reqx + 生成 client 的两种接入方式：
//   - nil factory 明确报错；
//   - 静态 Seeds 能发起真实 HTTP 调用。
func TestNewFromFactory(t *testing.T) {
	serverCalled := false
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer mockServer.Close()

	factory := reqx.NewFactory(nil, nil)

	// 1. Nil factory should fail
	_, err := catalogclient.NewFromFactory(nil, reqx.ClientInput{})
	assert.Error(t, err)

	// 2. Valid client with static Seeds
	client, err := catalogclient.NewFromFactory(factory, reqx.ClientInput{
		Name:  "catalog",
		Seeds: []string{mockServer.Listener.Addr().String()},
	})
	require.NoError(t, err)
	require.NotNil(t, client)

	// 3. Make real call via generated client
	resp, err := client.ListarticlesWithResponse(context.Background(), &catalogclient.ListarticlesParams{})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode())
	assert.True(t, serverCalled)
}
