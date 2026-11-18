package cachex

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache 提供基于 Redis 的分布式 Cache 实现。
type RedisCache struct {
	sfHolder

	client redis.UniversalClient
}

// NewRedisCache 使用给定的 go-redis 客户端创建 RedisCache。
func NewRedisCache(client redis.UniversalClient) *RedisCache {
	return &RedisCache{client: client}
}

// Client 返回底层封装的 redis.UniversalClient，便于高级命令操作。
func (r *RedisCache) Client() redis.UniversalClient {
	return r.client
}

// Ping 探测 Redis 连通性，供 /readyz 健康检查使用。
func (r *RedisCache) Ping(ctx context.Context) error {
	if r == nil || r.client == nil {
		return errors.New("cachex: redis client is nil")
	}
	return r.client.Ping(ctx).Err()
}

// Close 关闭底层 Redis 客户端。
//
// 由 [New] 创建的 Redis 客户端在模块关闭时由 Lifecycle 自动关闭；
// 调用方自行注入客户端时，关闭责任仍归注入方。
func (r *RedisCache) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

func (r *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
	val, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrCacheMiss
		}
		return nil, err
	}
	return val, nil
}

func (r *RedisCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return r.client.Set(ctx, key, val, normalizeRedisTTL(ttl)).Err()
}

// normalizeRedisTTL 统一 Redis 与 Cache 接口的 TTL 语义。
//
// go-redis 把负 TTL 解释为 KEEPTTL（保留原过期时间），而 Cache 接口约定
// ttl <= 0 表示永不过期。这里把负数归一到 0。
func normalizeRedisTTL(ttl time.Duration) time.Duration {
	if ttl < 0 {
		return 0
	}
	return ttl
}

func (r *RedisCache) Delete(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}

func (r *RedisCache) MGet(ctx context.Context, keys ...string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return map[string][]byte{}, nil
	}
	vals, err := r.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	res := make(map[string][]byte, len(keys))
	for i, v := range vals {
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			res[keys[i]] = []byte(val)
		case []byte:
			res[keys[i]] = val
		}
	}
	return res, nil
}

func (r *RedisCache) MSet(ctx context.Context, pairs map[string][]byte, ttl time.Duration) error {
	if len(pairs) == 0 {
		return nil
	}
	if ttl <= 0 {
		args := make([]any, 0, len(pairs)*2)
		for k, v := range pairs {
			args = append(args, k, v)
		}
		return r.client.MSet(ctx, args...).Err()
	}

	// Redis 原生 MSET 不支持 TTL，通过 pipeline 批量 SET
	pipe := r.client.Pipeline()
	for k, v := range pairs {
		pipe.Set(ctx, k, v, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}
