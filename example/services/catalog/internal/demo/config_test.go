package demo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fitan/fxkit-example/services/catalog/internal/demo"
)

// TestDynamicConfigUsesWatchedValue 验证有 Dynamic 句柄时优先使用热更新值。
func TestDynamicConfigUsesWatchedValue(t *testing.T) {
	c := &demo.DemoConfig{Welcome: "from consul"}
	svc, _ := newDemoService(t, demoDeps{dynamic: staticDynamic[demo.DemoConfig]{v: c}})

	got := svc.DynamicConfig()
	require.Equal(t, "from consul", got.Welcome)
	require.Equal(t, "dynamic", got.Source)
}

// TestDynamicConfigFallsBackToStatic 验证未启用 watch 时回退到启动快照。
func TestDynamicConfigFallsBackToStatic(t *testing.T) {
	svc, _ := newDemoService(t, demoDeps{})

	got := svc.DynamicConfig()
	require.Equal(t, "hello fxkit", got.Welcome)
	require.Equal(t, "static", got.Source)
}
