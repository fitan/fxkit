package demo_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fitan/fxkit-example/services/catalog/internal/demo"
)

// TestCacheGetOrSetCachesAndSingleflights 验证第二次命中缓存、不再回源。
func TestCacheGetOrSetCachesAndSingleflights(t *testing.T) {
	svc, _ := newDemoService(t, demoDeps{cache: newMemCache()})
	ctx := context.Background()

	first, err := svc.CacheGetOrSet(ctx, "k1")
	require.NoError(t, err)
	require.Equal(t, int64(1), first.Loads)

	// 第二次命中缓存：不应再执行 loader。
	second, err := svc.CacheGetOrSet(ctx, "k1")
	require.NoError(t, err)
	require.Equal(t, first.LoadedAt, second.LoadedAt)
	require.Equal(t, int64(1), second.Loads)
}

// TestCacheGetOrSetMergesConcurrentLoads 验证并发请求被 singleflight 合并为一次回源。
func TestCacheGetOrSetMergesConcurrentLoads(t *testing.T) {
	svc, _ := newDemoService(t, demoDeps{cache: newMemCache()})
	ctx := context.Background()

	const n = 8
	var wg sync.WaitGroup
	results := make([]demo.CacheDemo, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.CacheGetOrSet(ctx, "shared")
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		require.Equal(t, results[0].LoadedAt, results[i].LoadedAt)
	}
	require.Equal(t, int64(1), results[0].Loads)
}

// TestCacheGetOrSetRequiresName 验证 name 为空时返回校验错误。
func TestCacheGetOrSetRequiresName(t *testing.T) {
	svc, _ := newDemoService(t, demoDeps{})
	_, err := svc.CacheGetOrSet(context.Background(), "  ")
	require.Error(t, err)
}
