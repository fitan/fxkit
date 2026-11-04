package consulx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/consulx/internal/fakeconsul"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryLock_UnlockIsIdempotent 是回归测试：
// 早期 Unlock 无条件向缓冲 channel 写入令牌，重复 Unlock 会永久阻塞（且持有锁内部 mutex）。
func TestMemoryLock_UnlockIsIdempotent(t *testing.T) {
	l := consulx.NewMemoryLocker()
	ctx := context.Background()

	lock, err := l.NewLock("k")
	require.NoError(t, err)
	require.NoError(t, lock.Lock(ctx))
	require.NoError(t, lock.Unlock(ctx))

	// 第二次 Unlock：必须返回 ErrLockNotHeld，而不是阻塞
	done := make(chan error, 1)
	go func() { done <- lock.Unlock(ctx) }()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, consulx.ErrLockNotHeld)
	case <-time.After(time.Second):
		t.Fatal("second Unlock blocked forever (lock token channel was already full)")
	}

	// 锁必须能被其他持有者重新获取（令牌没有被吞掉或翻倍）
	other, err := l.NewLock("k")
	require.NoError(t, err)
	tctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	require.NoError(t, other.Lock(tctx))
	assert.True(t, other.IsHeld())
}

// TestMemoryLock_NonReentrant 同一实例再次 TryLock 返回 ErrLockHeld（不可重入）。
func TestMemoryLock_NonReentrant(t *testing.T) {
	l := consulx.NewMemoryLocker()
	ctx := context.Background()

	lock, _ := l.NewLock("k")
	require.NoError(t, lock.TryLock(ctx))
	assert.ErrorIs(t, lock.TryLock(ctx), consulx.ErrLockHeld)

	other, _ := l.NewLock("k")
	assert.ErrorIs(t, other.TryLock(ctx), consulx.ErrLockHeld)
}

// TestWithLock_ReleasesOnPanic 确保 WithLock 在 fn panic 时仍释放锁。
func TestWithLock_ReleasesOnPanic(t *testing.T) {
	l := consulx.NewMemoryLocker()
	ctx := context.Background()

	func() {
		defer func() { _ = recover() }()
		_ = l.WithLock(ctx, "k", func(context.Context) error {
			panic("boom")
		})
	}()

	lock, _ := l.NewLock("k")
	tctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	require.NoError(t, lock.Lock(tctx), "lock leaked after panic in WithLock")
}

// TestConsulWithLock_JoinsUnlockError 确保 fn 与 Unlock 的错误都会返回。
func TestConsulWithLock_JoinsUnlockError(t *testing.T) {
	fake := fakeconsul.New()
	locker, err := consulx.NewConsulLocker("http://consul.test", fake.Client())
	require.NoError(t, err)

	fnErr := errors.New("business failed")
	fake.ReleaseStatus = 500

	err = locker.WithLock(context.Background(), "k", func(context.Context) error {
		return fnErr
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, fnErr)
	assert.Contains(t, err.Error(), "release failed")
}
