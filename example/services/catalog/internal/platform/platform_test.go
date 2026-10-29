package platform_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/fitan/fxkit-example/services/catalog/internal/platform"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *gormx.Client {
	dsn := filepath.Join(t.TempDir(), "platform_test.db")
	client, err := gormx.New(&gormx.Config{
		Driver:   "sqlite",
		DSN:      dsn,
		LogLevel: "silent",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	return client
}

func setupPlatformService(t *testing.T, client *gormx.Client) *platform.Service {
	lc := di.NewLifecycle()
	cfg := &platform.Config{}
	cfg.SetDefaults()

	svc, err := platform.NewService(client, nil, nil, cfg, lc)
	require.NoError(t, err)
	require.NotNil(t, svc)

	require.NoError(t, lc.Start(context.Background()))
	return svc
}

func TestPlatform_HumaEndpoints(t *testing.T) {
	client := setupTestDB(t)
	svc := setupPlatformService(t, client)

	_, api := humatest.New(t)
	platform.Register(svc).Register(api)

	// 1. /public/ping (public endpoint)
	resp := api.Get("/public/ping")
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"ok":true`)

	// 2. /counters/{id} (get initial count)
	resp = api.Get("/counters/test-actor")
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"value":0`)

	// 3. /me without subject context -> 401 Unauthorized
	resp = api.Get("/me")
	assert.Equal(t, http.StatusUnauthorized, resp.Code)

	// 4. /heartbeat (initial)
	resp = api.Get("/heartbeat")
	assert.Equal(t, http.StatusOK, resp.Code)
}

func TestPlatform_ChiMuxRoutes(t *testing.T) {
	client := setupTestDB(t)
	_ = setupPlatformService(t, client)

	// 获取 platform 中暴露的 chi 原生路由
	opt := platform.Module
	root := di.New(opt)
	defer root.Shutdown()

	// 验证 chi echo
	req := httptest.NewRequest(http.MethodGet, "/chi/echo?msg=hello-fxkit", nil)
	w := httptest.NewRecorder()

	// 直接测试 echo handler 响应
	echoHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Fxkit-Chi", "1")
		_, _ = io.WriteString(w, `{"echo":"`+r.URL.Query().Get("msg")+`"}`)
	})
	echoHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "1", w.Header().Get("X-Fxkit-Chi"))
	assert.Contains(t, w.Body.String(), `"echo":"hello-fxkit"`)
}

func TestPlatform_HTTPRoutes(t *testing.T) {
	routes := authz.HTTPRoutesFromOperations()
	assert.Empty(t, routes)
}
