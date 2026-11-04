package consulx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/server"
)

// 需要真实 Consul（dev agent）：FXKIT_TEST_CONSUL_ADDR=http://127.0.0.1:8500

func consulAddrForTest(t *testing.T) string {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("FXKIT_TEST_CONSUL_ADDR"))
	if addr == "" {
		t.Skip("set FXKIT_TEST_CONSUL_ADDR to run Consul integration tests")
	}
	return addr
}

func TestConsulLockIntegration(t *testing.T) {
	addr := consulAddrForTest(t)
	ctx := context.Background()
	key := "fxkit-itest/" + t.Name()

	l, err := NewConsulLocker(addr, nil)
	if err != nil {
		t.Fatalf("locker: %v", err)
	}

	lock, err := l.NewLock(key, WithTTL(10*time.Second))
	if err != nil {
		t.Fatalf("new lock: %v", err)
	}
	if err := lock.TryLock(ctx); err != nil {
		t.Fatalf("trylock: %v", err)
	}
	if !lock.IsHeld() {
		t.Fatal("lock should be held")
	}

	// 不可重入：同一实例再次加锁直接 ErrLockHeld。
	if err := lock.TryLock(ctx); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("same instance re-lock: %v", err)
	}

	// 另一个实例无法抢占。
	l2, err := NewConsulLocker(addr, nil)
	if err != nil {
		t.Fatalf("locker2: %v", err)
	}
	lock2, err := l2.NewLock(key)
	if err != nil {
		t.Fatalf("new lock2: %v", err)
	}
	if err := lock2.TryLock(ctx); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("contended trylock: %v", err)
	}

	if err := lock.Unlock(ctx); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if lock.IsHeld() {
		t.Fatal("lock should be released")
	}
	if err := lock.Unlock(ctx); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("double unlock: %v", err)
	}

	// 释放后其他实例可以获取（允许短暂传播延迟）。
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = lock2.TryLock(ctx)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock not released: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := lock2.Unlock(ctx); err != nil {
		t.Fatalf("unlock2: %v", err)
	}

	// WithLock 自动释放。
	ran := false
	if err := l.WithLock(ctx, key+"-withlock", func(context.Context) error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("withlock: %v", err)
	}
	if !ran {
		t.Fatal("fn not executed")
	}
	lock3, err := l2.NewLock(key + "-withlock")
	if err != nil {
		t.Fatalf("new lock3: %v", err)
	}
	if err := lock3.TryLock(ctx); err != nil {
		t.Fatalf("withlock should release: %v", err)
	}
	_ = lock3.Unlock(ctx)

	// key 含 Consul 允许的特殊字符（? & =）也必须能正常加解锁。
	special, err := l.NewLock("k?x=1&y=2")
	if err != nil {
		t.Fatalf("special key: %v", err)
	}
	if err := special.TryLock(ctx); err != nil {
		t.Fatalf("special trylock: %v", err)
	}
	if err := special.Unlock(ctx); err != nil {
		t.Fatalf("special unlock: %v", err)
	}

	// 空格 / # 不在 Consul 允许字符集内 → 本地就报清晰错误（而不是服务端 500）。
	for _, bad := range []string{"has space", "hash#tag"} {
		if _, err := l.NewLock(bad); !errors.Is(err, ErrInvalidLockKey) {
			t.Fatalf("key %q: want ErrInvalidLockKey, got %v", bad, err)
		}
	}
}

func TestConsulRegisterSelfIntegration(t *testing.T) {
	addr := consulAddrForTest(t)
	ctx := context.Background()

	disc := &Config{
		ConsulAddress:    addr,
		Register:         true,
		AdvertiseAddress: "10.1.2.3", // 显式地址，避免 loopback 校验
	}
	app := &config.App{Name: "fxkit-itest-register"}
	srv := &server.Config{Port: "18080"}

	id, err := registerSelf(ctx, disc, app, srv)
	if err != nil {
		t.Fatalf("registerSelf: %v", err)
	}
	if id == "" {
		t.Fatal("empty service id")
	}
	t.Cleanup(func() { _ = deregisterSelf(context.Background(), disc, id) })

	base, err := normalizeConsulAddr(addr)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	resp, err := http.Get(base + "/v1/agent/service/" + id)
	if err != nil {
		t.Fatalf("get service: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("service lookup status=%d body=%s", resp.StatusCode, raw)
	}
	var payload struct {
		ID      string `json:"ID"`
		Service string `json:"Service"`
		Port    int    `json:"Port"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if payload.Service != "fxkit-itest-register" || payload.Port != 18080 {
		t.Fatalf("unexpected service: %+v", payload)
	}

	if err := deregisterSelf(ctx, disc, id); err != nil {
		t.Fatalf("deregister: %v", err)
	}
}
