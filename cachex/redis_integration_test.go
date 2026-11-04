package cachex

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// 需要真实 Redis：FXKIT_TEST_REDIS_ADDR=127.0.0.1:6379
// 使用 DB 3 并在前后 FlushDB，避免污染其它库。

func redisCacheForTest(t *testing.T) *RedisCache {
	t.Helper()
	addr := os.Getenv("FXKIT_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set FXKIT_TEST_REDIS_ADDR to run Redis integration tests")
	}
	cli := redis.NewClient(&redis.Options{Addr: addr, DB: 3})
	ctx := context.Background()
	if err := cli.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush db: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	t.Cleanup(func() { _ = cli.FlushDB(context.Background()).Err() })
	return NewRedisCache(cli)
}

func TestRedisIntegration_BasicOps(t *testing.T) {
	c := redisCacheForTest(t)
	ctx := context.Background()
	key := "fxkit:itest:basic"

	if err := c.Set(ctx, key, []byte("v1"), time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := c.Get(ctx, key)
	if err != nil || string(got) != "v1" {
		t.Fatalf("get: %q err=%v", got, err)
	}

	if err := c.MSet(ctx, map[string][]byte{"a": []byte("1"), "b": []byte("2")}, time.Minute); err != nil {
		t.Fatalf("mset: %v", err)
	}
	m, err := c.MGet(ctx, "a", "b", "missing")
	if err != nil {
		t.Fatalf("mget: %v", err)
	}
	if string(m["a"]) != "1" || string(m["b"]) != "2" {
		t.Fatalf("mget: %v", m)
	}
	if _, ok := m["missing"]; ok {
		t.Fatalf("missing key should be absent: %v", m)
	}

	if err := c.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := c.Get(ctx, key); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("want ErrCacheMiss, got %v", err)
	}

	// TTL 过期
	if err := c.Set(ctx, "ttl", []byte("x"), 300*time.Millisecond); err != nil {
		t.Fatalf("set ttl: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := c.Get(ctx, "ttl"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("ttl key should expire, got %v", err)
	}
}

func TestRedisIntegration_GetOrSetNegativeAndSingleflight(t *testing.T) {
	c := redisCacheForTest(t)
	ctx := context.Background()

	// 负缓存：loader 返回 gorm.ErrRecordNotFound，命中后仍保持 errors.Is 语义。
	var loads atomic.Int64
	loadNotFound := func(context.Context) (string, error) {
		loads.Add(1)
		return "", gorm.ErrRecordNotFound
	}
	_, err := GetOrSet(ctx, c, "fxkit:itest:neg", loadNotFound)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("first call: %v", err)
	}
	_, err = GetOrSet(ctx, c, "fxkit:itest:neg", loadNotFound)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("negative hit must keep ErrRecordNotFound, got %v", err)
	}
	if n := loads.Load(); n != 1 {
		t.Fatalf("negative cache miss: loader called %d times", n)
	}

	// singleflight：并发 8 个请求只触发一次 loader。
	var sfLoads atomic.Int64
	loader := func(context.Context) (string, error) {
		sfLoads.Add(1)
		time.Sleep(80 * time.Millisecond)
		return "value", nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := GetOrSet(ctx, c, "fxkit:itest:sf", loader)
			if err != nil || v != "value" {
				t.Errorf("getorset: %q err=%v", v, err)
			}
		}()
	}
	wg.Wait()
	if n := sfLoads.Load(); n != 1 {
		t.Fatalf("singleflight: loader called %d times, want 1", n)
	}
}
