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

	prefix string
	cache  Cache
}

func wrapPrefix(prefix string, cache Cache) Cache {
	if prefix == "" || cache == nil {
		return cache
	}
	return &prefixedCache{
		prefix: prefix,
		cache:  cache,
	}
}

func (p *prefixedCache) k(key string) string {
	return p.prefix + key
}

func (p *prefixedCache) unk(key string) string {
	return strings.TrimPrefix(key, p.prefix)
}

func (p *prefixedCache) Get(ctx context.Context, key string) ([]byte, error) {
	return p.cache.Get(ctx, p.k(key))
}

func (p *prefixedCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return p.cache.Set(ctx, p.k(key), val, ttl)
}

func (p *prefixedCache) Delete(ctx context.Context, key string) error {
	return p.cache.Delete(ctx, p.k(key))
}

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
