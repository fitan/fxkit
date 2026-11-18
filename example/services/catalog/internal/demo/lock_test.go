package demo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fitan/fxkit/consulx"
)

// TestLockDemoWithMemoryLocker 用内存实现验证锁 key 过滤与 fencing token。
func TestLockDemoWithMemoryLocker(t *testing.T) {
	svc, _ := newDemoService(t, demoDeps{locker: consulx.NewMemoryLocker()})

	out, err := svc.LockDemo(context.Background(), "order 42", 10*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, "demo/order-42", out.Key)
	require.NotZero(t, out.FencingToken)
}

// TestLockDemoDisabled 验证未配置 Consul 时明确报错，而不是静默成功。
func TestLockDemoDisabled(t *testing.T) {
	locker, err := consulx.BuildLocker(nil)
	require.NoError(t, err)

	svc, _ := newDemoService(t, demoDeps{locker: locker})
	_, err = svc.LockDemo(context.Background(), "x", 0)
	require.Error(t, err)
}
