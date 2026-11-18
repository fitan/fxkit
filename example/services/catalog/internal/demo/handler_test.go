package demo_test

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"

	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/outbox"

	"github.com/fitan/fxkit-example/services/catalog/internal/demo"
)

// TestDemoHumaEndpoints 验证各 demo 接口的状态码：启用 200、未启用 503。
func TestDemoHumaEndpoints(t *testing.T) {
	svc, _ := newDemoService(t, demoDeps{
		cache:  newMemCache(),
		locker: consulx.NewMemoryLocker(),
		obcfg:  &outbox.Config{Enabled: true},
	})
	_, api := humatest.New(t)
	demo.Register(svc).Register(api)

	resp := api.Get("/demo/config")
	require.Equal(t, http.StatusOK, resp.Code)
	require.Contains(t, resp.Body.String(), "hello fxkit")

	resp = api.Get("/demo/cache/foo")
	require.Equal(t, http.StatusOK, resp.Code)
	require.Contains(t, resp.Body.String(), "loaded-1")

	resp = api.Post("/demo/lock/foo", struct {
		// HoldMs 是人为持锁时长。
		HoldMs int `json:"holdMs"`
	}{HoldMs: 10})
	require.Equal(t, http.StatusOK, resp.Code)
	require.Contains(t, resp.Body.String(), "fencingToken")

	resp = api.Get("/demo/outbox/failed?limit=10")
	require.Equal(t, http.StatusOK, resp.Code)

	resp = api.Post("/demo/tx")
	require.Equal(t, http.StatusOK, resp.Code)
	require.Contains(t, resp.Body.String(), `"committed":true`)

	// Hatchet 未启用：明确 503。
	resp = api.Post("/demo/hatchet/dag", demo.DemoInput{Name: "x"})
	require.Equal(t, http.StatusServiceUnavailable, resp.Code)
}
