package cachex

import (
	"context"
	"sync"
	"time"
)

type memoryItem struct {
	val       []byte
	expiresAt time.Time // isZero() 表示永不过期
}

// MemoryCache 提供基于内存并发安全的 Cache 实现，适用于单机部署、本地测试与 Mock。
type MemoryCache struct {
	mu    sync.RWMutex
	items map[string]memoryItem
}

// NewMemoryCache 创建一个全新的内存缓存实例。
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		items: make(map[string]memoryItem),
	}
}

func (m *MemoryCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	item, ok := m.items[key]
	if !ok {
		return nil, ErrCacheMiss
	}
	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		return nil, ErrCacheMiss
	}
	// 返回副本防止外部修改污染
	out := make([]byte, len(item.val))
	copy(out, item.val)
	return out, nil
}

func (m *MemoryCache) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	cpy := make([]byte, len(val))
	copy(cpy, val)
	m.items[key] = memoryItem{
		val:       cpy,
		expiresAt: exp,
	}
	return nil
}

func (m *MemoryCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.items, key)
	return nil
}

func (m *MemoryCache) MGet(ctx context.Context, keys ...string) (map[string][]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make(map[string][]byte, len(keys))
	now := time.Now()
	for _, k := range keys {
		item, ok := m.items[k]
		if !ok {
			continue
		}
		if !item.expiresAt.IsZero() && now.After(item.expiresAt) {
			continue
		}
		out := make([]byte, len(item.val))
		copy(out, item.val)
		res[k] = out
	}
	return res, nil
}

func (m *MemoryCache) MSet(ctx context.Context, pairs map[string][]byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	for k, val := range pairs {
		cpy := make([]byte, len(val))
		copy(cpy, val)
		m.items[k] = memoryItem{
			val:       cpy,
			expiresAt: exp,
		}
	}
	return nil
}

// DisabledCache 当配置未启用缓存时使用的空实现，所有调用均返回 ErrCacheDisabled。
type DisabledCache struct{}

func (DisabledCache) Get(_ context.Context, _ string) ([]byte, error) {
	return nil, ErrCacheDisabled
}

func (DisabledCache) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return ErrCacheDisabled
}

func (DisabledCache) Delete(_ context.Context, _ string) error {
	return ErrCacheDisabled
}

func (DisabledCache) MGet(_ context.Context, _ ...string) (map[string][]byte, error) {
	return nil, ErrCacheDisabled
}

func (DisabledCache) MSet(_ context.Context, _ map[string][]byte, _ time.Duration) error {
	return ErrCacheDisabled
}
