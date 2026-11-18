package cachex

import (
	"context"
	"sync"
	"time"
)

// testCache 是测试专用的最小内存实现。
//
// cachex 生产只支持分布式 Redis；这里仅用于 helper/resolver 单测，
// 不对外暴露，也不参与 Module 的驱动选择。
type testCache struct {
	sfHolder

	mu    sync.Mutex
	items map[string]testItem
}

type testItem struct {
	val []byte
	exp time.Time
}

func newTestCache() *testCache {
	return &testCache{items: make(map[string]testItem)}
}

func (c *testCache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok {
		return nil, ErrCacheMiss
	}
	if !item.exp.IsZero() && time.Now().After(item.exp) {
		delete(c.items, key)
		return nil, ErrCacheMiss
	}
	out := make([]byte, len(item.val))
	copy(out, item.val)
	return out, nil
}

func (c *testCache) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	item := testItem{val: append([]byte(nil), val...)}
	if ttl > 0 {
		item.exp = time.Now().Add(ttl)
	}
	c.items[key] = item
	return nil
}

func (c *testCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
	return nil
}

func (c *testCache) MGet(_ context.Context, keys ...string) (map[string][]byte, error) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		item, ok := c.items[k]
		if !ok {
			continue
		}
		if !item.exp.IsZero() && now.After(item.exp) {
			delete(c.items, k)
			continue
		}
		out[k] = append([]byte(nil), item.val...)
	}
	return out, nil
}

func (c *testCache) MSet(_ context.Context, pairs map[string][]byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	for k, v := range pairs {
		c.items[k] = testItem{val: append([]byte(nil), v...), exp: exp}
	}
	return nil
}
