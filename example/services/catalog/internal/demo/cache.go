package demo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fitan/fxkit/cachex"
	"github.com/fitan/fxkit/fxerrors"
)

const cacheDemoKeyPrefix = "fxkit:demo:cache:"

// cacheDemoValue 是真正被 JSON 序列化进缓存的负载。
type cacheDemoValue struct {
	// Value 是缓存值。
	Value string `json:"value"`
	// LoadedAt 是 loader 执行时间。
	LoadedAt time.Time `json:"loadedAt"`
}

// CacheGetOrSet 演示 cachex.GetOrSet：
// 缓存命中直接返回；未命中时 loader 回源，并由 singleflight 合并并发请求。
func (s *Service) CacheGetOrSet(ctx context.Context, name string) (CacheDemo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CacheDemo{}, fxerrors.Validation(map[string]string{"name": "required"})
	}
	key := cacheDemoKeyPrefix + name
	v, err := cachex.GetOrSet(ctx, s.cache, key, func(ctx context.Context) (cacheDemoValue, error) {
		// 放慢 loader：便于观察并发请求是否被合并为一次回源。
		select {
		case <-ctx.Done():
			return cacheDemoValue{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		n := s.loads.Add(1)
		return cacheDemoValue{Value: fmt.Sprintf("loaded-%d", n), LoadedAt: time.Now().UTC()}, nil
	}, cachex.WithTTL(30*time.Second), cachex.WithJitter(10), cachex.WithLoadTimeout(2*time.Second))
	if err != nil {
		return CacheDemo{}, fxerrors.Wrap(err)
	}
	return CacheDemo{Key: key, Value: v.Value, LoadedAt: v.LoadedAt, Loads: s.loads.Load()}, nil
}
