package cachex

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetOrSet_NegativeCacheKeepsNotFoundSemantics 是回归测试：
// 命中空值缓存时必须返回「未找到」语义，而不是 ErrCacheMiss。
// 否则调用方 `errors.Is(err, gorm.ErrRecordNotFound)` 在首访和命中缓存时行为不一致。
func TestGetOrSet_NegativeCacheKeepsNotFoundSemantics(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache()

	var loads int64
	notFound := errors.New("record not found")
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
	a := NewMemoryCache()
	b := NewMemoryCache()

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
	c := NewMemoryCache()
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
