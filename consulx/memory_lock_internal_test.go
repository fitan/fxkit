package consulx

import (
	"context"
	"testing"
	"time"
)

func newInternalMemoryLocker() *memoryLocker {
	return NewMemoryLocker().(*memoryLocker)
}

// P1-10：引用计数归零后 entry 必须被删除，避免无限增长。
func TestMemoryLocker_EntryReclaimed(t *testing.T) {
	locker := newInternalMemoryLocker()
	ctx := context.Background()

	l, err := locker.NewLock("k")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	if n := locker.entryCount(); n != 1 {
		t.Fatalf("expected 1 live entry while held, got %d", n)
	}
	if err := l.Unlock(ctx); err != nil {
		t.Fatal(err)
	}
	if n := locker.entryCount(); n != 0 {
		t.Fatalf("entry not reclaimed after Unlock: %d", n)
	}

	// 冲突失败也不能留下悬挂引用。
	holder, _ := locker.NewLock("k2")
	if err := holder.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	other, _ := locker.NewLock("k2")
	if err := other.TryLock(ctx); err != ErrLockHeld {
		t.Fatalf("expected ErrLockHeld, got %v", err)
	}
	if n := locker.entryCount(); n != 1 {
		t.Fatalf("expected only holder entry, got %d", n)
	}
	_ = holder.Unlock(ctx)
	if n := locker.entryCount(); n != 0 {
		t.Fatalf("entry not reclaimed: %d", n)
	}
}

// P1-10：Prefix 参与内存实现的命名空间，与 Consul 实现保持一致。
func TestMemoryLocker_PrefixIsolation(t *testing.T) {
	locker := NewMemoryLocker()
	ctx := context.Background()

	a, _ := locker.NewLock("same", WithPrefix("tenant-a/"))
	b, _ := locker.NewLock("same", WithPrefix("tenant-b/"))
	if err := a.TryLock(ctx); err != nil {
		t.Fatalf("tenant-a lock: %v", err)
	}
	if err := b.TryLock(ctx); err != nil {
		t.Fatalf("different prefix must not conflict: %v", err)
	}

	c, _ := locker.NewLock("same", WithPrefix("tenant-a/"))
	if err := c.TryLock(ctx); err != ErrLockHeld {
		t.Fatalf("same prefix must conflict, got %v", err)
	}

	_ = a.Unlock(ctx)
	_ = b.Unlock(ctx)
	if err := c.TryLock(ctx); err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = c.Unlock(ctx)
}

// P1-10：ctx 已取消时优先返回取消错误，且不泄漏 entry。
func TestMemoryLocker_CancelPreferredOverToken(t *testing.T) {
	locker := newInternalMemoryLocker()
	l, err := locker.NewLock("k")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Lock(ctx); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if l.IsHeld() {
		t.Fatal("lock must not be held after canceled Lock")
	}
	if n := locker.entryCount(); n != 0 {
		t.Fatalf("entry leaked after canceled Lock: %d", n)
	}

	// 阻塞期间取消同样优先于令牌就绪。
	holder, _ := locker.NewLock("k2")
	if err := holder.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	waiter, _ := locker.NewLock("k2")
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer waitCancel()
	if err := waiter.Lock(waitCtx); err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	_ = holder.Unlock(context.Background())
	if n := locker.entryCount(); n != 0 {
		t.Fatalf("entry leaked after canceled waiter: %d", n)
	}
}
