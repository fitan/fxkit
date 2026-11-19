package cachex

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// prefixedCache 为所有 Cache 操作透明添加 Key 前缀。
type prefixedCache struct {
	sfHolder

	// prefix 是统一添加的键前缀。
	prefix string
	// cache 是被包装的底层缓存。
	cache Cache
}

// wrapPrefix 在 prefix 非空且 cache 非 nil 时用 prefixedCache 包装，否则原样返回 cache。
func wrapPrefix(prefix string, cache Cache) Cache {
	if prefix == "" || cache == nil {
		return cache
	}
	return &prefixedCache{
		prefix: prefix,
		cache:  cache,
	}
}

// k 返回加上前缀后的完整 key。
func (p *prefixedCache) k(key string) string {
	return p.prefix + key
}

// unk 去掉 key 的前缀。
func (p *prefixedCache) unk(key string) string {
	return strings.TrimPrefix(key, p.prefix)
}

// Get 给 key 加前缀后读取底层缓存。
func (p *prefixedCache) Get(ctx context.Context, key string) ([]byte, error) {
	return p.cache.Get(ctx, p.k(key))
}

// Set 给 key 加前缀后写入底层缓存。
func (p *prefixedCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return p.cache.Set(ctx, p.k(key), val, ttl)
}

// Delete 给 key 加前缀后删除底层缓存。
func (p *prefixedCache) Delete(ctx context.Context, key string) error {
	return p.cache.Delete(ctx, p.k(key))
}

// MGet 给所有 key 加前缀后批量读取，并把结果 key 还原为不带前缀的形式；空入参返回空 map。
func (p *prefixedCache) MGet(ctx context.Context, keys ...string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return map[string][]byte{}, nil
	}
	prefixedKeys := make([]string, len(keys))
	for i, k := range keys {
		prefixedKeys[i] = p.k(k)
	}
	res, err := p.cache.MGet(ctx, prefixedKeys...)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(res))
	for k, v := range res {
		out[p.unk(k)] = v
	}
	return out, nil
}

// MSet 给所有 key 加前缀后批量写入；空入参直接返回 nil。
func (p *prefixedCache) MSet(ctx context.Context, pairs map[string][]byte, ttl time.Duration) error {
	if len(pairs) == 0 {
		return nil
	}
	prefixedPairs := make(map[string][]byte, len(pairs))
	for k, v := range pairs {
		prefixedPairs[p.k(k)] = v
	}
	return p.cache.MSet(ctx, prefixedPairs, ttl)
}

// Close 转发底层缓存的 Close（若支持），使模块关闭时能释放 Redis 连接。
func (p *prefixedCache) Close() error {
	if c, ok := p.cache.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

// Ping 转发底层缓存的连通性探测。
//
// 必须显式转发：健康检查按能力（Ping）而不是按具体类型（*RedisCache）判定，
// 否则配置了 cache.prefix 后包装层会让探测永远通过。
func (p *prefixedCache) Ping(ctx context.Context) error {
	if q, ok := p.cache.(interface{ Ping(context.Context) error }); ok {
		return q.Ping(ctx)
	}
	return fmt.Errorf("cache: underlying driver %T does not support ping", p.cache)
}
