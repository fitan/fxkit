package demo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fitan/fxkit-example/services/catalog/internal/demo"
)

// 未配置 Hatchet 时，相关接口必须明确返回「不可用」，而不是 panic 或静默成功。
func TestHatchetUnavailableWhenDisabled(t *testing.T) {
	svc, _ := newDemoService(t, demoDeps{})
	ctx := context.Background()

	_, err := svc.ScheduleOnceDemo(ctx, demo.DemoInput{Name: "x"})
	require.Error(t, err)

	_, err = svc.RunDAGDemo(ctx, demo.DemoInput{Name: "x"})
	require.Error(t, err)

	_, err = svc.RunDurableDemo(ctx, demo.DemoInput{Name: "x"})
	require.Error(t, err)

	err = svc.SetRateLimitDemo(ctx, demo.RateLimitReq{Key: "k", Limit: 1})
	require.Error(t, err)
}
