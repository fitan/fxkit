package demo_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fitan/fxkit/cachex"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	"github.com/fitan/fxkit/outbox"

	"github.com/fitan/fxkit-example/services/catalog/internal/demo"
)

// memCache 是测试用的最小 Cache 实现（生产用 Redis 或 DisabledCache）。
type memCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

// newMemCache 创建测试用的内存缓存。
func newMemCache() *memCache { return &memCache{m: make(map[string][]byte)} }

// Get 未命中时返回 cachex.ErrCacheMiss。
func (c *memCache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	if !ok {
		return nil, cachex.ErrCacheMiss
	}
	return append([]byte(nil), v...), nil
}

// Set 忽略 TTL（测试不依赖过期）。
func (c *memCache) Set(_ context.Context, key string, val []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = append([]byte(nil), val...)
	return nil
}

// Delete 删除单 key。
func (c *memCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
	return nil
}

// MGet 逐 key 读取，只返回命中的项。
func (c *memCache) MGet(ctx context.Context, keys ...string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		if v, err := c.Get(ctx, k); err == nil {
			out[k] = v
		}
	}
	return out, nil
}

// MSet 逐 key 写入。
func (c *memCache) MSet(ctx context.Context, pairs map[string][]byte, ttl time.Duration) error {
	for k, v := range pairs {
		if err := c.Set(ctx, k, v, ttl); err != nil {
			return err
		}
	}
	return nil
}

// staticDynamic 是固定值的 config.Dynamic 测试实现。
type staticDynamic[T any] struct{ v *T }

// Get 返回构造时提供的快照。
func (d staticDynamic[T]) Get() *T { return d.v }

// demoDeps 列出各测试需要注入的可选依赖。
type demoDeps struct {
	cache   cachex.Cache
	locker  consulx.Locker
	dynamic config.Dynamic[demo.DemoConfig]
	obcfg   *outbox.Config
	hatchet *hatchetx.Client
}

// newDemoService 用独立 SQLite 与给定依赖构造 demo.Service。
func newDemoService(t *testing.T, deps demoDeps) (*demo.Service, *gormx.Client) {
	t.Helper()
	client, err := gormx.New(&gormx.Config{
		Driver:   "sqlite",
		DSN:      filepath.Join(t.TempDir(), "demo.db"),
		LogLevel: "silent",
	})
	require.NoError(t, err)
	require.NoError(t, client.Conn(context.Background()).AutoMigrate(&outbox.OutboxEvent{}))

	cfg := &demo.DemoConfig{}
	cfg.SetDefaults()
	obcfg := deps.obcfg
	if obcfg == nil {
		obcfg = &outbox.Config{Enabled: false}
	}
	ht := deps.hatchet
	if ht == nil {
		ht = &hatchetx.Client{}
	}
	svc, err := demo.NewService(deps.cache, deps.dynamic, cfg, deps.locker, outbox.NewStore(client), obcfg, ht, client)
	require.NoError(t, err)
	return svc, client
}
