package cachex

import (
	"context"
	"sync"
	"time"
)

// memorySweepEvery 是惰性清理的触发阈值：每写入 N 条触发一次过期扫描。
// MemoryCache 没有后台 goroutine，因此不会泄漏；同时避免大量过期 key 长期占用内存。
const memorySweepEvery = 256

type memoryItem struct {
	val       []byte
	expiresAt time.Time // isZero() 表示永不过期
}

// MemoryCache 提供基于内存并发安全的 Cache 实现，适用于单机部署、本地测试与 Mock。
type MemoryCache struct {
	mu    sync.RWMutex
	items map[string]memoryItem

	// writesSinceSweep 统计写入次数，达到阈值时惰性清理过期项。
	writesSinceSweep int
}

// NewMemoryCache 创建一个全新的内存缓存实例。
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		items: make(map[string]memoryItem),
	}
}

func (m *MemoryCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	item, ok := m.items[key]
	if !ok {
		m.mu.RUnlock()
		return nil, ErrCacheMiss
	}
	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		m.mu.RUnlock()
		// 命中过期项时顺手删除，避免过期数据无限堆积。
		m.deleteIfExpired(key)
		return nil, ErrCacheMiss
	}
	// 返回副本防止外部修改污染
	out := make([]byte, len(item.val))
	copy(out, item.val)
	m.mu.RUnlock()
	return out, nil
}

func (m *MemoryCache) deleteIfExpired(key string) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if item, ok := m.items[key]; ok && !item.expiresAt.IsZero() && now.After(item.expiresAt) {
		delete(m.items, key)
	}
}

func (m *MemoryCache) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	now := time.Now()
	var exp time.Time
	if ttl > 0 {
		exp = now.Add(ttl)
	}

	cpy := make([]byte, len(val))
	copy(cpy, val)

	m.mu.Lock()
	m.items[key] = memoryItem{
		val:       cpy,
		expiresAt: exp,
	}
	m.writesSinceSweep++
	if m.writesSinceSweep >= memorySweepEvery {
		m.sweepExpiredLocked(now)
		m.writesSinceSweep = 0
	}
	m.mu.Unlock()
	return nil
}

func (m *MemoryCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.items, key)
	return nil
}

func (m *MemoryCache) MGet(_ context.Context, keys ...string) (map[string][]byte, error) {
	now := time.Now()

	m.mu.RLock()
	res := make(map[string][]byte, len(keys))
	var expired []string
	for _, k := range keys {
		item, ok := m.items[k]
		if !ok {
			continue
		}
		if !item.expiresAt.IsZero() && now.After(item.expiresAt) {
			expired = append(expired, k)
			continue
		}
		out := make([]byte, len(item.val))
		copy(out, item.val)
		res[k] = out
	}
	m.mu.RUnlock()

	if len(expired) > 0 {
		m.mu.Lock()
		for _, k := range expired {
			if item, ok := m.items[k]; ok && !item.expiresAt.IsZero() && now.After(item.expiresAt) {
				delete(m.items, k)
			}
		}
		m.mu.Unlock()
	}
	return res, nil
}

func (m *MemoryCache) MSet(_ context.Context, pairs map[string][]byte, ttl time.Duration) error {
	now := time.Now()
	var exp time.Time
	if ttl > 0 {
		exp = now.Add(ttl)
	}

	m.mu.Lock()
	for k, val := range pairs {
		cpy := make([]byte, len(val))
		copy(cpy, val)
		m.items[k] = memoryItem{
			val:       cpy,
			expiresAt: exp,
		}
	}
	m.writesSinceSweep += len(pairs)
	if m.writesSinceSweep >= memorySweepEvery {
		m.sweepExpiredLocked(now)
		m.writesSinceSweep = 0
	}
	m.mu.Unlock()
	return nil
}

// SweepExpired 立即清理所有已过期的 key，并返回清理数量。
//
// Get/MGet 与写入阈值触发的惰性清理通常足够；长时间运行且 key 基数很大时，
// 可以由调用方（例如定时任务）主动调用此方法。
func (m *MemoryCache) SweepExpired() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sweepExpiredLocked(time.Now())
}

func (m *MemoryCache) sweepExpiredLocked(now time.Time) int {
	removed := 0
	for k, item := range m.items {
		if !item.expiresAt.IsZero() && now.After(item.expiresAt) {
			delete(m.items, k)
			removed++
		}
	}
	return removed
}

// Len 返回当前缓存条目数量（含尚未被清理的过期项），主要用于观测与测试。
func (m *MemoryCache) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
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
