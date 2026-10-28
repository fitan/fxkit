package cachex

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrCacheMiss 表示缓存未命中。
	ErrCacheMiss = errors.New("cache: key not found")
	// ErrCacheDisabled 表示缓存未配置或已被禁用。
	ErrCacheDisabled = errors.New("cache: disabled")
)

// Cache 定义通用缓存读写接口。
type Cache interface {
	// Get 获取指定 key 对应的数据。若不存在则返回 ErrCacheMiss。
	Get(ctx context.Context, key string) ([]byte, error)
	// Set 设置指定 key 的数据及过期时间。ttl <= 0 表示永不过期。
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	// Delete 删除指定 key。若 key 不存在亦返回 nil。
	Delete(ctx context.Context, key string) error

	// MGet 批量获取多个 key。返回 map 中仅包含成功命中的 key-value。
	MGet(ctx context.Context, keys ...string) (map[string][]byte, error)
	// MSet 批量写入多个 key-value，并统一设置过期时间。
	MSet(ctx context.Context, pairs map[string][]byte, ttl time.Duration) error
}
