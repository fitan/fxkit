package cachex

import (
	"context"
	"time"
)

// DisabledCache 当配置未启用缓存时使用的空实现，所有调用均返回 ErrCacheDisabled。
type DisabledCache struct{}

// Get 始终返回 nil 和 ErrCacheDisabled。
func (DisabledCache) Get(_ context.Context, _ string) ([]byte, error) {
	return nil, ErrCacheDisabled
}

// Set 始终返回 ErrCacheDisabled。
func (DisabledCache) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return ErrCacheDisabled
}

// Delete 始终返回 ErrCacheDisabled。
func (DisabledCache) Delete(_ context.Context, _ string) error {
	return ErrCacheDisabled
}

// MGet 始终返回 nil 和 ErrCacheDisabled。
func (DisabledCache) MGet(_ context.Context, _ ...string) (map[string][]byte, error) {
	return nil, ErrCacheDisabled
}

// MSet 始终返回 ErrCacheDisabled。
func (DisabledCache) MSet(_ context.Context, _ map[string][]byte, _ time.Duration) error {
	return ErrCacheDisabled
}
