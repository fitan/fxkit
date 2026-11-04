package consulx_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/consulx/internal/fakeconsul"
)

func TestMemoryLocker_Basic(t *testing.T) {
	locker := consulx.NewMemoryLocker()
	ctx := context.Background()

	l, err := locker.NewLock("test-job")
	if err != nil {
		t.Fatalf("NewLock error: %v", err)
	}
	if l.Key() != "test-job" {
		t.Fatalf("expected key test-job, got %s", l.Key())
	}
	if l.IsHeld() {
		t.Fatalf("expected not held initially")
	}

	if err := l.Lock(ctx); err != nil {
		t.Fatalf("Lock error: %v", err)
	}
	if !l.IsHeld() {
		t.Fatalf("expected held after Lock")
	}

	if err := l.Unlock(ctx); err != nil {
		t.Fatalf("Unlock error: %v", err)
	}
	if l.IsHeld() {
		t.Fatalf("expected not held after Unlock")
	}

	// 重复解锁应返回 ErrLockNotHeld
	if err := l.Unlock(ctx); err != consulx.ErrLockNotHeld {
		t.Fatalf("expected ErrLockNotHeld, got %v", err)
	}
}

func TestMemoryLocker_TryLock_MutualExclusion(t *testing.T) {
	locker := consulx.NewMemoryLocker()
	ctx := context.Background()

	l1, err := locker.NewLock("order:123")
	if err != nil {
		t.Fatal(err)
	}
	l2, err := locker.NewLock("order:123")
	if err != nil {
		t.Fatal(err)
	}

	if err := l1.TryLock(ctx); err != nil {
		t.Fatalf("l1 TryLock failed: %v", err)
	}
	if err := l2.TryLock(ctx); err != consulx.ErrLockHeld {
		t.Fatalf("expected ErrLockHeld for l2, got %v", err)
	}

	if err := l1.Unlock(ctx); err != nil {
		t.Fatalf("l1 Unlock failed: %v", err)
	}
	if err := l2.TryLock(ctx); err != nil {
		t.Fatalf("l2 TryLock after l1 released failed: %v", err)
	}
	_ = l2.Unlock(ctx)
}

// 同一实例不可重入：重复 Lock/TryLock 返回 ErrLockHeld。
func TestMemoryLocker_NonReentrant(t *testing.T) {
	locker := consulx.NewMemoryLocker()
	ctx := context.Background()

	l, err := locker.NewLock("reentrant")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.TryLock(ctx); err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	if err := l.TryLock(ctx); err != consulx.ErrLockHeld {
		t.Fatalf("second TryLock: expected ErrLockHeld, got %v", err)
	}
	if err := l.Lock(ctx); err != consulx.ErrLockHeld {
		t.Fatalf("Lock while held: expected ErrLockHeld, got %v", err)
	}
	if err := l.Unlock(ctx); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}
}

// 同一实例被多个 goroutine 共享时，绝不能同时进入临界区。
func TestMemoryLocker_NoConcurrentCriticalSection(t *testing.T) {
	locker := consulx.NewMemoryLocker()
	l, err := locker.NewLock("critical")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	var mu sync.Mutex
	var wg sync.WaitGroup
	inside, maxInside := 0, 0
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if err := l.TryLock(ctx); err != nil {
					continue
				}
				mu.Lock()
				inside++
				if inside > maxInside {
					maxInside = inside
				}
				mu.Unlock()
				time.Sleep(50 * time.Microsecond)
				mu.Lock()
				inside--
				mu.Unlock()
				_ = l.Unlock(ctx)
			}
		}()
	}
	wg.Wait()
	if maxInside > 1 {
		t.Fatalf("critical section entered by more than one goroutine: %d", maxInside)
	}
	if maxInside == 0 {
		t.Fatal("lock was never acquired")
	}
}

func TestMemoryLocker_WithLock(t *testing.T) {
	locker := consulx.NewMemoryLocker()
	ctx := context.Background()

	executed := false
	err := locker.WithLock(ctx, "task:clean", func(c context.Context) error {
		executed = true
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock failed: %v", err)
	}
	if !executed {
		t.Fatalf("WithLock fn was not executed")
	}

	l, _ := locker.NewLock("task:clean")
	if err := l.TryLock(ctx); err != nil {
		t.Fatalf("expected lock to be released after WithLock: %v", err)
	}
	_ = l.Unlock(ctx)
}

func newConsulLocker(t *testing.T, fake *fakeconsul.Server) consulx.Locker {
	t.Helper()
	locker, err := consulx.NewConsulLocker("http://consul.test", fake.Client())
	if err != nil {
		t.Fatalf("NewConsulLocker failed: %v", err)
	}
	return locker
}

func TestConsulLocker_FakeConsul(t *testing.T) {
	fake := fakeconsul.New()
	locker := newConsulLocker(t, fake)
	ctx := context.Background()

	// 1. 基本 TryLock 与 Unlock
	l1, err := locker.NewLock("sync-node-1", consulx.WithPrefix("custom/"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l1.TryLock(ctx); err != nil {
		t.Fatalf("l1 TryLock failed: %v", err)
	}
	if !l1.IsHeld() {
		t.Fatalf("expected l1 to be held")
	}

	// 2. 互斥冲突
	l2, err := locker.NewLock("sync-node-1", consulx.WithPrefix("custom/"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l2.TryLock(ctx); err != consulx.ErrLockHeld {
		t.Fatalf("expected ErrLockHeld for l2, got: %v", err)
	}

	// 3. 释放后重新争用
	if err := l1.Unlock(ctx); err != nil {
		t.Fatalf("l1 Unlock failed: %v", err)
	}
	if l1.IsHeld() {
		t.Fatalf("expected l1 not held after unlock")
	}
	if err := l2.TryLock(ctx); err != nil {
		t.Fatalf("l2 TryLock should succeed after l1 unlock: %v", err)
	}
	_ = l2.Unlock(ctx)

	// 4. WithLock 验证
	withLockRan := false
	err = locker.WithLock(ctx, "sync-closure", func(c context.Context) error {
		withLockRan = true
		return nil
	}, consulx.WithValue([]byte("worker-pid-42")))
	if err != nil {
		t.Fatalf("WithLock failed: %v", err)
	}
	if !withLockRan {
		t.Fatalf("WithLock function did not run")
	}
}

func TestConsulLocker_NonReentrant(t *testing.T) {
	fake := fakeconsul.New()
	locker := newConsulLocker(t, fake)
	ctx := context.Background()

	l, _ := locker.NewLock("nr")
	if err := l.TryLock(ctx); err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	if err := l.TryLock(ctx); err != consulx.ErrLockHeld {
		t.Fatalf("second TryLock: expected ErrLockHeld, got %v", err)
	}
	if err := l.Lock(ctx); err != consulx.ErrLockHeld {
		t.Fatalf("Lock while held: expected ErrLockHeld, got %v", err)
	}
	if err := l.Unlock(ctx); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}
}

func TestConsulLocker_BlockingAndTimeout(t *testing.T) {
	fake := fakeconsul.New()
	locker := newConsulLocker(t, fake)
	ctx := context.Background()

	l1, _ := locker.NewLock("sync-blocking")
	if err := l1.TryLock(ctx); err != nil {
		t.Fatalf("l1 TryLock failed: %v", err)
	}

	// l2 在较短超时时间内阻塞获取同一个 key，应当因 ctx 超时返回 DeadlineExceeded
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	l2, _ := locker.NewLock("sync-blocking", consulx.WithRetryWait(20*time.Millisecond))
	if err := l2.Lock(timeoutCtx); err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}

	// l1 释放后，l2 在新的 context 下阻塞等待成功获取锁
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = l1.Unlock(context.Background())
	}()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()

	if err := l2.Lock(waitCtx); err != nil {
		t.Fatalf("l2 Lock after l1 async unlock failed: %v", err)
	}
	if !l2.IsHeld() {
		t.Fatalf("expected l2 to be held")
	}
	_ = l2.Unlock(context.Background())
}

func TestConsulLocker_Validation(t *testing.T) {
	if _, err := consulx.NewConsulLocker("", nil); err != consulx.ErrConsulDisabled {
		t.Fatalf("expected ErrConsulDisabled on empty address, got: %v", err)
	}

	locker, err := consulx.NewConsulLocker("http://localhost:8500", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = locker.NewLock("   "); err == nil {
		t.Fatalf("expected error on empty key")
	}
}

func TestProvideLocker_DisabledFallback(t *testing.T) {
	locker := consulx.ProvideLocker(&consulx.Config{ConsulAddress: ""})
	if locker == nil {
		t.Fatal("expected non-nil Locker")
	}

	ctx := context.Background()
	if _, err := locker.NewLock("any"); err != consulx.ErrConsulDisabled {
		t.Fatalf("expected ErrConsulDisabled on NewLock, got: %v", err)
	}
	if err := locker.WithLock(ctx, "any", func(c context.Context) error { return nil }); err != consulx.ErrConsulDisabled {
		t.Fatalf("expected ErrConsulDisabled on WithLock, got: %v", err)
	}
}

// 地址非法时 BuildLocker 必须返回错误，而不是静默降级。
func TestBuildLocker_InvalidAddress(t *testing.T) {
	if _, err := consulx.BuildLocker(&consulx.Config{ConsulAddress: "http://[::1"}); err == nil {
		t.Fatal("expected error for invalid consul address")
	}

	// 兼容 API：ProvideLocker 仍然返回非 nil，但调用会暴露配置错误。
	locker := consulx.ProvideLocker(&consulx.Config{ConsulAddress: "http://[::1"})
	if locker == nil {
		t.Fatal("expected non-nil Locker")
	}
	_, err := locker.NewLock("any")
	if err == nil || !errors.Is(err, consulx.ErrConsulDisabled) {
		t.Fatalf("expected ErrConsulDisabled-wrapped error, got %v", err)
	}
}

func TestWithLockResult(t *testing.T) {
	mem := consulx.NewMemoryLocker()
	ctx := context.Background()

	res, err := consulx.WithLockResult(mem, ctx, "calc-key", func(c context.Context) (int, error) {
		return 999, nil
	})
	if err != nil || res != 999 {
		t.Fatalf("expected 999, got %d %v", res, err)
	}

	res, err = consulx.WithLockResult(mem, ctx, "calc-key-err", func(c context.Context) (int, error) {
		return 0, errors.New("computation failed")
	})
	if err == nil || res != 0 {
		t.Fatalf("expected error and 0, got %d %v", res, err)
	}
}
