package cachex

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestGetOrSet_NegativeCacheKeepsNotFoundSemantics 是回归测试：
// 命中空值缓存时返回 ErrNegativeHit，而不是再打 loader，也不是 ErrCacheMiss。
func TestGetOrSet_NegativeCacheKeepsNotFoundSemantics(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()

	var loads int64
	notFound := gorm.ErrRecordNotFound
	loader := func(context.Context) (string, error) {
		atomic.AddInt64(&loads, 1)
		return "", notFound
	}

	_, err := GetOrSet(ctx, c, "k", loader, WithNullTTL(time.Minute))
	require.Error(t, err)
	assert.False(t, IsNegativeHit(err), "first call ran the loader, it is not a negative hit")

	_, err = GetOrSet(ctx, c, "k", loader, WithNullTTL(time.Minute))
	require.Error(t, err)
	assert.True(t, IsNegativeHit(err), "second call should hit the negative entry, got %v", err)
	assert.False(t, errors.Is(err, ErrCacheMiss), "negative hit must not look like a plain cache miss")
	assert.Equal(t, int64(1), atomic.LoadInt64(&loads), "loader must not run again")
}

// TestGetOrSet_SingleflightIsolatedPerCache 是回归测试：
// singleflight 早期是包级全局变量，只用业务 key 做键，导致不同 cache 实例串结果。
func TestGetOrSet_SingleflightIsolatedPerCache(t *testing.T) {
	ctx := context.Background()
	a := newTestCache()
	b := newTestCache()

	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		_, _ = GetOrSet(ctx, a, "same-key", func(context.Context) (string, error) {
			close(started)
			time.Sleep(80 * time.Millisecond)
			return "from-A", nil
		})
	}()
	<-started
	time.Sleep(10 * time.Millisecond)

	v, err := GetOrSet(ctx, b, "same-key", func(context.Context) (string, error) {
		return "from-B", nil
	})
	<-done

	require.NoError(t, err)
	assert.Equal(t, "from-B", v, "singleflight must not leak results across cache instances")
}

// TestResolverUsesConfig 是回归测试：
// Config 的 default_ttl / null_ttl / jitter / singleflight 早期完全没被 GetOrSet 读取。
func TestResolverUsesConfig(t *testing.T) {
	ctx := context.Background()
	c := newTestCache()
	cfg := &Config{DefaultTTL: time.Millisecond}
	cfg.SetDefaults()
	cfg.DefaultTTL = 20 * time.Millisecond
	cfg.NullTTL = 20 * time.Millisecond
	cfg.JitterMaxPercent = 0 // 关闭抖动，便于断言精确过期

	r := NewResolver(c, cfg)

	var loads int64
	loader := func(context.Context) (string, error) {
		atomic.AddInt64(&loads, 1)
		return "v", nil
	}

	_, err := r.GetOrSet(ctx, "k", loader)
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&loads))

	// 在 default_ttl 内应命中
	_, err = r.GetOrSet(ctx, "k", loader)
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&loads))

	// 超过 default_ttl 后应重新加载 —— 证明配置生效（旧实现硬编码 30m）
	time.Sleep(40 * time.Millisecond)
	_, err = r.GetOrSet(ctx, "k", loader)
	require.NoError(t, err)
	assert.Equal(t, int64(2), atomic.LoadInt64(&loads), "default_ttl from config was ignored")
}

// TestGetOrSet_BackendErrorFallsBackToLoader 缓存后端故障不应阻断业务。
func TestGetOrSet_BackendErrorFallsBackToLoader(t *testing.T) {
	ctx := context.Background()
	c := &brokenCache{}
	v, err := GetOrSet(ctx, c, "k", func(context.Context) (string, error) { return "live", nil })
	require.NoError(t, err)
	assert.Equal(t, "live", v)
}

// TestGetOrSet_BackendErrorStillSingleflights 是回归测试：
// 降级回源必须仍然经过 singleflight。早期实现直接 `return loader(ctx)`，
// 于是 Redis 抖动、并发压力最大的那一刻，所有请求同时打到数据源。
func TestGetOrSet_BackendErrorStillSingleflights(t *testing.T) {
	ctx := context.Background()
	c := &brokenPtrCache{}

	var loads int64
	loader := func(context.Context) (string, error) {
		atomic.AddInt64(&loads, 1)
		time.Sleep(100 * time.Millisecond)
		return "live", nil
	}

	const n = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			v, err := GetOrSet(ctx, c, "k", loader)
			assert.NoError(t, err)
			assert.Equal(t, "live", v)
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int64(1), atomic.LoadInt64(&loads),
		"cache backend failure must still collapse concurrent loads into one")
}

// TestCacheHealthChecker_ReportsErrorThroughPrefixWrapper 是回归测试：
// Check 早期对 *RedisCache 做类型断言，而配了 cache.prefix 后缓存会被
// *prefixedCache 包住，断言失败直接 return nil —— /readyz 的 cache 检查永远健康。
func TestCacheHealthChecker_ReportsErrorThroughPrefixWrapper(t *testing.T) {
	inner := &pingableCache{pingErr: errors.New("redis down")}
	c := wrapPrefix("app:", inner)

	err := cacheHealthChecker{cache: c}.Check(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis down")
}

// TestCacheHealthChecker_HealthyThroughPrefixWrapper 包装层不能把健康检查变成永久通过。
func TestCacheHealthChecker_HealthyThroughPrefixWrapper(t *testing.T) {
	inner := &pingableCache{}
	c := wrapPrefix("app:", inner)

	require.NoError(t, cacheHealthChecker{cache: c}.Check(context.Background()))
}

type brokenCache struct{}

func (brokenCache) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("redis: connection refused")
}
func (brokenCache) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (brokenCache) Delete(context.Context, string) error                     { return nil }
func (brokenCache) MGet(context.Context, ...string) (map[string][]byte, error) {
	return nil, nil
}
func (brokenCache) MSet(context.Context, map[string][]byte, time.Duration) error { return nil }

// brokenPtrCache 用指针接收者：singleflight 按实例地址合并，非指针实现不合并。
type brokenPtrCache struct{ brokenCache }

func (brokenPtrCache) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("redis: connection refused")
}

// pingableCache 模拟支持连通性探测的驱动。
type pingableCache struct {
	brokenCache
	pingErr error
}

func (p *pingableCache) Ping(context.Context) error { return p.pingErr }

// TestOptionsFromConfig_ExplicitZeroNullTTLHonored 是回归测试：
//
// 早期用「四个字段全为零」判断「未配置」，于是 yaml 里
// `null_ttl: 0` + `singleflight: false`（想关闭负缓存与单飞）会让整段配置被忽略。
// 现在由 SetDefaults 打标记区分「已加载的配置」与「代码里直接构造的零值」。
func TestOptionsFromConfig_ExplicitZeroNullTTLHonored(t *testing.T) {
	cfg := &Config{}
	cfg.SetDefaults()
	cfg.NullTTL = 0          // 显式关闭空值缓存
	cfg.Singleflight = false // 显式关闭单飞

	opt := OptionsFromConfig(cfg)
	if opt.NullTTL != 0 {
		t.Fatalf("NullTTL = %v, want 0 (explicitly disabled)", opt.NullTTL)
	}
	if !opt.DisableSingleflight {
		t.Fatal("explicit singleflight: false must be honored")
	}
}
