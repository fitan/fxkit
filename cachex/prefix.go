package cachex

import (
	"context"
	"strings"
	"time"
)

// prefixedCache 为所有 Cache 操作透明添加 Key 前缀。
type prefixedCache struct {
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
