package cachex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryCache_Basic(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache()

	// 1. Get not found
	_, err := c.Get(ctx, "k1")
	assert.ErrorIs(t, err, ErrCacheMiss)

	// 2. Set and Get
	err = c.Set(ctx, "k1", []byte("val1"), time.Hour)
	require.NoError(t, err)

	val, err := c.Get(ctx, "k1")
	require.NoError(t, err)
	assert.Equal(t, "val1", string(val))

	// 3. Delete
	err = c.Delete(ctx, "k1")
	require.NoError(t, err)
	_, err = c.Get(ctx, "k1")
	assert.ErrorIs(t, err, ErrCacheMiss)

	// 4. Expiration
	err = c.Set(ctx, "k_exp", []byte("val_exp"), 10*time.Millisecond)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	_, err = c.Get(ctx, "k_exp")
	assert.ErrorIs(t, err, ErrCacheMiss)
}

func TestMemoryCache_Batch(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache()

	pairs := map[string][]byte{
		"k1": []byte("v1"),
		"k2": []byte("v2"),
	}
	err := c.MSet(ctx, pairs, time.Minute)
	require.NoError(t, err)

	res, err := c.MGet(ctx, "k1", "k2", "k3")
	require.NoError(t, err)
	assert.Len(t, res, 2)
	assert.Equal(t, "v1", string(res["k1"]))
	assert.Equal(t, "v2", string(res["k2"]))
}

func TestPrefixWrapper(t *testing.T) {
	ctx := context.Background()
	mem := NewMemoryCache()
	c := wrapPrefix("test:", mem)

	err := c.Set(ctx, "foo", []byte("bar"), time.Hour)
	require.NoError(t, err)

	// Direct check on underlying memory
	val, err := mem.Get(ctx, "test:foo")
	require.NoError(t, err)
	assert.Equal(t, "bar", string(val))

	// Get via prefixed wrapper
	val, err = c.Get(ctx, "foo")
	require.NoError(t, err)
	assert.Equal(t, "bar", string(val))

	// MGet via prefixed wrapper
	res, err := c.MGet(ctx, "foo", "none")
	require.NoError(t, err)
	assert.Equal(t, "bar", string(res["foo"]))
	_, exists := res["none"]
	assert.False(t, exists)
}

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func TestGetOrSet_SingleflightAndCache(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache()

	var loadCount int64

	loader := func(ctx context.Context) (User, error) {
		time.Sleep(50 * time.Millisecond) // 模拟慢查询
		atomic.AddInt64(&loadCount, 1)
		return User{ID: 10, Name: "Alice"}, nil
	}

	// 并发 10 个请求同时请求相同的 key，singleflight 应该只执行 1 次 loader
	const concurrency = 10
	done := make(chan struct{}, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			u, err := GetOrSet(ctx, c, "user:10", loader)
			assert.NoError(t, err)
			assert.Equal(t, 10, u.ID)
			assert.Equal(t, "Alice", u.Name)
		}()
	}

	for i := 0; i < concurrency; i++ {
		<-done
	}

	assert.Equal(t, int64(1), atomic.LoadInt64(&loadCount), "singleflight should coalesce concurrent calls to 1 loader execution")

	// 缓存已写入，再次获取不调用 loader
	u, err := GetOrSet(ctx, c, "user:10", func(ctx context.Context) (User, error) {
		t.Fatal("should not hit loader when cached")
		return User{}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "Alice", u.Name)
}

func TestGetOrSet_CacheNull(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache()

	var loadCount int64
	loader := func(ctx context.Context) (User, error) {
		atomic.AddInt64(&loadCount, 1)
		return User{}, errors.New("record not found")
	}

	// 第一次调用返回 record not found，并缓存空占位符
	_, err := GetOrSet(ctx, c, "user:404", loader, WithNullTTL(time.Minute))
	assert.Error(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&loadCount))

	// 第二次调用应当命中空值缓存，直接返回 ErrCacheMiss，而不会再次触发 loader（防穿透）
	_, err = GetOrSet(ctx, c, "user:404", func(ctx context.Context) (User, error) {
		t.Fatal("should not call loader for cached null")
		return User{}, nil
	})
	assert.ErrorIs(t, err, ErrCacheMiss)
	assert.Equal(t, int64(1), atomic.LoadInt64(&loadCount))
}

func TestDIModule(t *testing.T) {
	cfgYAML := `cache:
  driver: "memory"
  prefix: "app:"
  default_ttl: 10m
`
	tmp := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(tmp, []byte(cfgYAML), 0o600))

	cfg, err := config.New(config.Options{ConfigFile: tmp})
	require.NoError(t, err)

	injector := di.New()
	di.Provide(injector, func(i di.Injector) (*config.Config, error) {
		return cfg, nil
	})
	Module(injector)

	cacheInst, err := di.Invoke[Cache](injector)
	require.NoError(t, err)
	require.NotNil(t, cacheInst)

	ctx := context.Background()
	err = cacheInst.Set(ctx, "hello", []byte("world"), time.Minute)
	require.NoError(t, err)

	val, err := cacheInst.Get(ctx, "hello")
	require.NoError(t, err)
	assert.Equal(t, "world", string(val))
}
