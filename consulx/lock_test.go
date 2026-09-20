package consulx_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fitan/fxkit/consulx"
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

	// 再次解锁
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

	// l1 获取锁
	if err := l1.TryLock(ctx); err != nil {
		t.Fatalf("l1 TryLock failed: %v", err)
	}

	// l2 尝试获取锁，应当返回 ErrLockHeld
	if err := l2.TryLock(ctx); err != consulx.ErrLockHeld {
		t.Fatalf("expected ErrLockHeld for l2, got %v", err)
	}

	// l1 释放
	if err := l1.Unlock(ctx); err != nil {
		t.Fatalf("l1 Unlock failed: %v", err)
	}

	// l2 再次尝试获取锁，应当成功
	if err := l2.TryLock(ctx); err != nil {
		t.Fatalf("l2 TryLock after l1 released failed: %v", err)
	}
	_ = l2.Unlock(ctx)
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

	// 验证锁在闭包执行完毕后已经释放，能够再次被获取
	l, _ := locker.NewLock("task:clean")
	if err := l.TryLock(ctx); err != nil {
		t.Fatalf("expected lock to be released after WithLock: %v", err)
	}
	_ = l.Unlock(ctx)
}

func setupMockConsulServer() (*httptest.Server, func()) {
	var mu sync.Mutex
	var sessionSeq uint64
	sessions := make(map[string]bool)
	kvLocks := make(map[string]string) // key -> sessionID

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		mu.Lock()
		defer mu.Unlock()

		// 1. Session 创建
		if path == "/v1/session/create" && r.Method == http.MethodPut {
			id := atomic.AddUint64(&sessionSeq, 1)
			sessionID := fmt.Sprintf("session-%d", id)
			sessions[sessionID] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": sessionID})
			return
		}

		// 2. Session 续期
		if strings.HasPrefix(path, "/v1/session/renew/") && r.Method == http.MethodPut {
			sid := strings.TrimPrefix(path, "/v1/session/renew/")
			if sessions[sid] {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}

		// 3. Session 销毁
		if strings.HasPrefix(path, "/v1/session/destroy/") && r.Method == http.MethodPut {
			sid := strings.TrimPrefix(path, "/v1/session/destroy/")
			delete(sessions, sid)
			for k, v := range kvLocks {
				if v == sid {
					delete(kvLocks, k)
				}
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		// 4. KV acquire/release
		if strings.HasPrefix(path, "/v1/kv/") {
			key := strings.TrimPrefix(path, "/v1/kv/")
			if acquire := r.URL.Query().Get("acquire"); acquire != "" {
				if current, ok := kvLocks[key]; !ok || current == acquire {
					kvLocks[key] = acquire
					_, _ = io.WriteString(w, "true")
					return
				}
				_, _ = io.WriteString(w, "false")
				return
			}
			if release := r.URL.Query().Get("release"); release != "" {
				if kvLocks[key] == release {
					delete(kvLocks, key)
					_, _ = io.WriteString(w, "true")
					return
				}
				_, _ = io.WriteString(w, "false")
				return
			}
			// GET kv
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))

	return server, server.Close
}

func TestConsulLocker_MockServer(t *testing.T) {
	server, cleanup := setupMockConsulServer()
	defer cleanup()

	locker, err := consulx.NewConsulLocker(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewConsulLocker failed: %v", err)
	}

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

func TestConsulLocker_BlockingAndTimeout(t *testing.T) {
	server, cleanup := setupMockConsulServer()
	defer cleanup()

	locker, err := consulx.NewConsulLocker(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewConsulLocker failed: %v", err)
	}

	ctx := context.Background()

	l1, _ := locker.NewLock("sync-blocking")
	if err := l1.TryLock(ctx); err != nil {
		t.Fatalf("l1 TryLock failed: %v", err)
	}

	// l2 在较短超时时间内阻塞获取同一个 key，应当因 ctx 超时返回 DeadlineExceeded
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	l2, _ := locker.NewLock("sync-blocking", consulx.WithRetryWait(20*time.Millisecond))
	err = l2.Lock(timeoutCtx)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}

	// l1 释放后，l2 在新的 context 下阻塞等待成功获取锁
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = l1.Unlock(context.Background())
	}()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
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
	_, err := consulx.NewConsulLocker("", nil)
	if err != consulx.ErrConsulDisabled {
		t.Fatalf("expected ErrConsulDisabled on empty address, got: %v", err)
	}

	locker, _ := consulx.NewConsulLocker("http://localhost:8500", nil)
	_, err = locker.NewLock("   ")
	if err == nil {
		t.Fatalf("expected error on empty key")
	}
}

func TestProvideLocker_DisabledFallback(t *testing.T) {
	// 当无配置或 ConsulAddress 为空时
	locker := consulx.ProvideLocker(&consulx.Config{ConsulAddress: ""})
	if locker == nil {
		t.Fatal("expected non-nil Locker")
	}

	ctx := context.Background()
	_, err := locker.NewLock("any")
	if err != consulx.ErrConsulDisabled {
		t.Fatalf("expected ErrConsulDisabled on NewLock, got: %v", err)
	}

	err = locker.WithLock(ctx, "any", func(c context.Context) error { return nil })
	if err != consulx.ErrConsulDisabled {
		t.Fatalf("expected ErrConsulDisabled on WithLock, got: %v", err)
	}
}
