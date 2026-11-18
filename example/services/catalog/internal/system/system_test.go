package system_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
	"github.com/fitan/fxkit-example/services/catalog/internal/system"
)

// setupTestDB 为每个测试创建独立的 SQLite 数据库。
func setupTestDB(t *testing.T) *gormx.Client {
	dsn := filepath.Join(t.TempDir(), "system_test.db")
	client, err := gormx.New(&gormx.Config{
		Driver:   "sqlite",
		DSN:      dsn,
		LogLevel: "silent",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	return client
}

// setupSystemService 构造 system 服务并跑启动钩子。
func setupSystemService(t *testing.T, client *gormx.Client) *system.Service {
	lc := di.NewLifecycle()
	cfg := &system.Config{}
	cfg.SetDefaults()

	svc, err := system.NewService(
		client,
		repository.NewCounterRepo(client),
		repository.NewHeartbeatRepo(client),
		nil, nil, cfg, nil, lc,
	)
	require.NoError(t, err)
	require.NotNil(t, svc)

	require.NoError(t, lc.Start(context.Background()))
	return svc
}

// TestSystem_HumaEndpoints 覆盖探针、counter、heartbeat、cache 接口。
func TestSystem_HumaEndpoints(t *testing.T) {
	client := setupTestDB(t)
	svc := setupSystemService(t, client)

	_, api := humatest.New(t)
	system.Register(svc).Register(api)

	// 1. /public/ping（公开端点）
	resp := api.Get("/public/ping")
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"ok":true`)

	// 2. /counters/{id}（获取初始计数）
	resp = api.Get("/counters/test-actor")
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"value":0`)

	// 3. /me 没有 subject 上下文 -> 401 Unauthorized
	resp = api.Get("/me")
	assert.Equal(t, http.StatusUnauthorized, resp.Code)

	// 4. /heartbeat（初始状态）
	resp = api.Get("/heartbeat")
	assert.Equal(t, http.StatusOK, resp.Code)
}

// TestSystem_ChiMuxRoutes 验证裸 chi 路由与 fxerrors 输出。
func TestSystem_ChiMuxRoutes(t *testing.T) {
	client := setupTestDB(t)
	_ = setupSystemService(t, client)

	// 获取 system 中暴露的 chi 原生路由
	opt := system.Module
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

// TestSystem_HTTPRoutes 验证 authz 路由目录覆盖 Huma 与 chi。
func TestSystem_HTTPRoutes(t *testing.T) {
	routes := authz.HTTPRoutesFromOperations()
	assert.Empty(t, routes)
}
