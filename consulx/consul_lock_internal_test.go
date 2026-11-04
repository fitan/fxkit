package consulx

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fitan/fxkit/consulx/internal/fakeconsul"
)

func newInternalLocker(t *testing.T, fake *fakeconsul.Server) *consulLocker {
	t.Helper()
	l, err := NewConsulLocker("http://consul.test", fake.Client())
	if err != nil {
		t.Fatalf("NewConsulLocker: %v", err)
	}
	cl, ok := l.(*consulLocker)
	if !ok {
		t.Fatalf("unexpected locker type %T", l)
	}
	return cl
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

func lockSession(l *consulLock) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.renew == nil {
		return ""
	}
	return l.renew.sessionID
}

// P0-1：续租协程在阻塞等待期间死亡后，Lock 不能沿用已失效的 session 报告成功；
// 必须重建 session 后重试（或返回错误）。
func TestConsulLock_RebuildsSessionAfterRenewDeath(t *testing.T) {
	fake := fakeconsul.New()
	locker := newInternalLocker(t, fake)

	holder, err := locker.NewLock("k")
	if err != nil {
		t.Fatal(err)
	}
	waiterIface, err := locker.NewLock("k", WithRetryWait(5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	waiter := waiterIface.(*consulLock)
	waiter.renewInterval = 3 * time.Millisecond

	if err := holder.TryLock(context.Background()); err != nil {
		t.Fatalf("holder TryLock: %v", err)
	}
	holderSession := fake.SessionFor("locks/k")

	lockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- waiter.Lock(lockCtx) }()

	// 等待 waiter 创建自己的 session，然后只让该 session 的续租失败。
	var waiterSession string
	if !waitUntil(t, time.Second, func() bool {
		waiterSession = fake.LastSessionID()
		return waiterSession != "" && waiterSession != holderSession
	}) {
		t.Fatal("waiter did not create a session")
	}
	fake.Doom(waiterSession)

	// 确认续租已连续失败（阈值 2），此时 waiter 的 session 在本地已判定丢失。
	if !waitUntil(t, time.Second, func() bool { return fake.RenewCount(waiterSession) >= renewFailureThreshold }) {
		t.Fatalf("waiter session renew was not exercised: %d", fake.RenewCount(waiterSession))
	}

	if err := holder.Unlock(context.Background()); err != nil {
		t.Fatalf("holder Unlock: %v", err)
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("waiter Lock should rebuild session and succeed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter Lock hung")
	}

	if !waiter.IsHeld() {
		t.Fatal("waiter should hold the lock after rebuild")
	}
	newSession := lockSession(waiter)
	if newSession == waiterSession {
		t.Fatalf("waiter reused the doomed session %q", waiterSession)
	}

	// 新 session 的续租是健康的：一段时间后仍应持有。
	time.Sleep(20 * time.Millisecond)
	if !waiter.IsHeld() {
		t.Fatal("lock lost right after rebuild")
	}
	if err := waiter.Unlock(context.Background()); err != nil {
		t.Fatalf("waiter Unlock: %v", err)
	}
}

// P0-1：所有续租都失败时，Lock 必须返回错误，而不是假装持锁。
func TestConsulLock_ErrorsWhenRenewAlwaysFails(t *testing.T) {
	fake := fakeconsul.New()
	fake.SetFailAllRenew(true)
	fake.SetAcquireDelay(15 * time.Millisecond)
	locker := newInternalLocker(t, fake)

	l, err := locker.NewLock("k", WithRetryWait(5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	cl := l.(*consulLock)
	cl.renewInterval = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := cl.Lock(ctx); err == nil {
		t.Fatal("Lock must not report success while renew goroutine is dead")
	}
	if cl.IsHeld() {
		t.Fatal("IsHeld must be false after failed Lock")
	}
}

// P0-1：TryLock 在续租协程已判定丢失时必须返回 ErrLockLost。
func TestConsulLock_TryLockDetectsDeadRenew(t *testing.T) {
	fake := fakeconsul.New()
	fake.SetFailAllRenew(true)
	fake.SetAcquireDelay(15 * time.Millisecond)
	locker := newInternalLocker(t, fake)

	l, err := locker.NewLock("k")
	if err != nil {
		t.Fatal(err)
	}
	cl := l.(*consulLock)
	cl.renewInterval = time.Millisecond

	err = cl.TryLock(context.Background())
	if !errors.Is(err, ErrLockLost) {
		t.Fatalf("expected ErrLockLost, got %v", err)
	}
	if cl.IsHeld() {
		t.Fatal("IsHeld must be false")
	}
}

// P0-2：Unlock 与续租失败分支并发时不得死锁。
func TestConsulLock_UnlockDoesNotDeadlock(t *testing.T) {
	fake := fakeconsul.New()
	locker := newInternalLocker(t, fake)

	for i := 0; i < 40; i++ {
		l, err := locker.NewLock("k")
		if err != nil {
			t.Fatal(err)
		}
		cl := l.(*consulLock)
		cl.renewInterval = time.Millisecond
		if err := cl.TryLock(context.Background()); err != nil {
			t.Fatalf("iteration %d TryLock: %v", i, err)
		}

		// 让续租在 Unlock 附近失败，复现「Unlock 持锁等待 renewDone / renewLoop 等 l.mu」的窗口。
		fake.SetFailAllRenew(true)
		time.Sleep(time.Duration(i%3) * time.Millisecond)

		done := make(chan error, 1)
		go func() { done <- cl.Unlock(context.Background()) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: Unlock deadlocked", i)
		}
		fake.SetFailAllRenew(false)
	}
}

// P1-6：5xx 应退避重试，4xx 应立即失败。
func TestConsulLock_AcquireRetries5xx(t *testing.T) {
	fake := fakeconsul.New()
	fake.AcquireFailure = 2
	locker := newInternalLocker(t, fake)

	l, err := locker.NewLock("k", WithRetryWait(5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.TryLock(context.Background()); err != nil {
		t.Fatalf("TryLock should retry 5xx and succeed: %v", err)
	}
	if acquire, _, _ := fake.Counts(); acquire != 3 {
		t.Fatalf("expected 3 acquire attempts, got %d", acquire)
	}
	_ = l.Unlock(context.Background())
}

func TestConsulLock_AcquireDoesNotRetry4xx(t *testing.T) {
	fake := fakeconsul.New()
	fake.AcquireStatus = 403
	locker := newInternalLocker(t, fake)

	l, err := locker.NewLock("k", WithRetryWait(5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	err = l.TryLock(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected 403 error, got %v", err)
	}
	if acquire, _, _ := fake.Counts(); acquire != 1 {
		t.Fatalf("fatal 4xx must not be retried, acquire calls=%d", acquire)
	}
	if fake.SessionCount() != 0 {
		t.Fatalf("session should be destroyed after fatal acquire error, live sessions=%d", fake.SessionCount())
	}
}

// P1-7：KV key 每段都做 PathEscape，避免空格 / # / ? / 非 ASCII 破坏路径或注入查询参数。
func TestConsulLock_KeyEscaping(t *testing.T) {
	fake := fakeconsul.New()
	locker := newInternalLocker(t, fake)

	// Consul 允许 '?' '&' '=' ',' 等字符，但它们在 URL 里必须被转义，
	// 否则会被当成查询参数。
	l, err := locker.NewLock("a?b=c&d/e,f")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.TryLock(context.Background()); err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	got := fake.LastEscapedPath()
	// 关键：'?' 必须转义（否则会变成查询参数）；'=' '&' ',' 在路径段里合法/按 Go 规则处理。
	want := "/v1/kv/locks/a%3Fb=c&d/e%2Cf"
	if got != want {
		t.Fatalf("escaped path mismatch:\n got=%q\nwant=%q", got, want)
	}

	if sid := fake.SessionFor("locks/a?b=c&d/e,f"); sid == "" {
		t.Fatal("acquire did not reach the expected decoded key")
	}
	_ = l.Unlock(context.Background())
}

// Consul 的 KV key 只接受 [a-zA-Z0-9,_./\-?&=]；空格 / # / 非 ASCII 必须在本地
// 就报清晰错误，而不是让服务端返回 500 "invalid key name"。
func TestConsulLock_InvalidKeyRejected(t *testing.T) {
	fake := fakeconsul.New()
	locker := newInternalLocker(t, fake)

	for _, key := range []string{"has space", "hash#tag", "中文"} {
		if _, err := locker.NewLock(key); !errors.Is(err, ErrInvalidLockKey) {
			t.Fatalf("key %q: want ErrInvalidLockKey, got %v", key, err)
		}
	}

	// prefix 也参与最终 KV 路径，同样要校验。
	if _, err := locker.NewLock("ok", WithPrefix("bad prefix/")); !errors.Is(err, ErrInvalidLockKey) {
		t.Fatalf("invalid prefix: want ErrInvalidLockKey, got %v", err)
	}
	if acquire, release, destroy := fake.Counts(); acquire+release+destroy != 0 {
		t.Fatalf("invalid keys must not reach Consul, got acquire=%d release=%d destroy=%d", acquire, release, destroy)
	}
}

// P1-8：TTL 低于 10s 被 clamp，且整秒转换向上取整。
func TestConsulLock_TTLClampAndCeil(t *testing.T) {
	cases := []struct {
		name string
		ttl  time.Duration
		want string
	}{
		{"clamp", 1500 * time.Millisecond, "10s|0s"},
		{"ceil", 11500 * time.Millisecond, "12s|0s"},
		{"exact", 10 * time.Second, "10s|0s"},
		{"delay ceil", 10500 * time.Millisecond, "11s|1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := fakeconsul.New()
			locker := newInternalLocker(t, fake)
			opts := []LockOption{WithTTL(tc.ttl)}
			if tc.name == "delay ceil" {
				opts = append(opts, WithLockDelay(500*time.Millisecond))
			}
			l, err := locker.NewLock("k", opts...)
			if err != nil {
				t.Fatal(err)
			}
			cl := l.(*consulLock)
			if err := cl.TryLock(context.Background()); err != nil {
				t.Fatalf("TryLock: %v", err)
			}
			sid := lockSession(cl)
			if got := fake.SessionTTL(sid); got != tc.want {
				t.Fatalf("session ttl=%q, want %q", got, tc.want)
			}
			_ = cl.Unlock(context.Background())
		})
	}
}

// P1-5：阻塞等待必须携带 X-Consul-Index 与 wait 参数。
func TestConsulLock_BlockingQueryUsesIndexAndWait(t *testing.T) {
	fake := fakeconsul.New()
	locker := newInternalLocker(t, fake)

	holder, err := locker.NewLock("k")
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.TryLock(context.Background()); err != nil {
		t.Fatal(err)
	}

	waiterIface, err := locker.NewLock("k", WithRetryWait(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- waiterIface.Lock(ctx) }()

	if !waitUntil(t, time.Second, func() bool {
		n, _ := fake.Watch()
		return n > 0
	}) {
		t.Fatal("no blocking query was issued")
	}
	_, q := fake.Watch()
	if q.Get("index") == "" {
		t.Fatalf("blocking query missing index: %v", q)
	}
	if q.Get("wait") == "" {
		t.Fatalf("blocking query missing wait: %v", q)
	}

	if err := holder.Unlock(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("waiter Lock: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter Lock hung")
	}
	_ = waiterIface.Unlock(context.Background())
}

// P1-4：release / destroy 失败必须返回错误。
func TestConsulLock_UnlockReportsCleanupErrors(t *testing.T) {
	t.Run("release", func(t *testing.T) {
		fake := fakeconsul.New()
		locker := newInternalLocker(t, fake)
		l, _ := locker.NewLock("k")
		if err := l.TryLock(context.Background()); err != nil {
			t.Fatal(err)
		}
		fake.ReleaseStatus = 500
		err := l.Unlock(context.Background())
		if err == nil || !strings.Contains(err.Error(), "release failed") {
			t.Fatalf("expected release error, got %v", err)
		}
		if l.IsHeld() {
			t.Fatal("lock must not be held after Unlock")
		}
	})

	t.Run("destroy", func(t *testing.T) {
		fake := fakeconsul.New()
		locker := newInternalLocker(t, fake)
		l, _ := locker.NewLock("k")
		if err := l.TryLock(context.Background()); err != nil {
			t.Fatal(err)
		}
		fake.DestroyStatus = 500
		err := l.Unlock(context.Background())
		if err == nil || !strings.Contains(err.Error(), "destroy failed") {
			t.Fatalf("expected destroy error, got %v", err)
		}
	})
}

// 冲突失败时必须销毁本次创建的 session，避免残留。
func TestConsulLock_TryLockConflictDestroysSession(t *testing.T) {
	fake := fakeconsul.New()
	locker := newInternalLocker(t, fake)

	holder, _ := locker.NewLock("k")
	if err := holder.TryLock(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, before := fake.Counts()

	other, _ := locker.NewLock("k")
	if err := other.TryLock(context.Background()); err != ErrLockHeld {
		t.Fatalf("expected ErrLockHeld, got %v", err)
	}
	_, _, after := fake.Counts()
	if after-before != 1 {
		t.Fatalf("expected 1 destroy after conflict, got %d", after-before)
	}
	_ = holder.Unlock(context.Background())
}

// P1-4：锁已因续租失败丢失时，Unlock 必须返回 ErrLockLost（而不是 nil）。
func TestConsulLock_UnlockAfterRenewLossReturnsLockLost(t *testing.T) {
	fake := fakeconsul.New()
	locker := newInternalLocker(t, fake)

	l, err := locker.NewLock("k")
	if err != nil {
		t.Fatal(err)
	}
	cl := l.(*consulLock)
	cl.renewInterval = time.Millisecond
	if err := cl.TryLock(context.Background()); err != nil {
		t.Fatalf("TryLock: %v", err)
	}

	fake.SetFailAllRenew(true)
	if !waitUntil(t, 2*time.Second, func() bool { return !cl.IsHeld() }) {
		t.Fatal("lock should be reported lost after renew failures")
	}

	err = cl.Unlock(context.Background())
	if !errors.Is(err, ErrLockLost) {
		t.Fatalf("expected ErrLockLost, got %v", err)
	}
	if cl.IsHeld() {
		t.Fatal("lock must not be held after Unlock")
	}
	if _, release, _ := fake.Counts(); release != 0 {
		t.Fatalf("release must be skipped when session already lost, release calls=%d", release)
	}
}

// P1-12：OnLost panic 不能终止进程，且只回调一次。
func TestConsulLock_OnLostPanicRecovered(t *testing.T) {
	fake := fakeconsul.New()
	locker := newInternalLocker(t, fake)

	called := make(chan struct{})
	var once sync.Once
	l, err := locker.NewLock("k", WithOnLost(func(key string, err error) {
		once.Do(func() { close(called) })
		panic("onlost boom")
	}))
	if err != nil {
		t.Fatal(err)
	}
	cl := l.(*consulLock)
	cl.renewInterval = 2 * time.Millisecond

	if err := cl.TryLock(context.Background()); err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	fake.SetFailAllRenew(true)

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("OnLost was not called")
	}
	if !waitUntil(t, 2*time.Second, func() bool { return !cl.IsHeld() }) {
		t.Fatal("lock should be reported lost")
	}
	// 给 panic 的 goroutine 一点时间：若未被 recover，测试进程会直接崩溃。
	time.Sleep(20 * time.Millisecond)
}
