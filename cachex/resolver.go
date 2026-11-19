package cachex

import (
	"context"
)

// Resolver 把模块级 [Config] 与 [GetOrSet] 绑定起来，
// 让 yaml 里的 default_ttl / null_ttl / singleflight / jitter_max_percent 真正生效。
//
// 用法：
//
//	user, err := resolver.GetOrSet(ctx, "user:1", loadUser)
//
// 或者对单次调用覆盖：resolver.GetOrSet(ctx, key, load, cachex.WithTTL(time.Minute))
type Resolver struct {
	// cache 是底层缓存实例。
	cache Cache
	// def 是来自模块配置的默认选项。
	def Options
}

// NewResolver 用配置构造 Resolver。cfg 为 nil 时使用内置默认值。
func NewResolver(c Cache, cfg *Config) *Resolver {
	return &Resolver{
		cache: c,
		def:   OptionsFromConfig(cfg),
	}
}

// Cache 返回底层 Cache 实例。
func (r *Resolver) Cache() Cache {
	if r == nil {
		return nil
	}
	return r.cache
}

// GetOrSet 等价于 [GetOrSet]，但以配置中的默认值作为基线。
func (r *Resolver) GetOrSet[T any](
	ctx context.Context,
	key string,
	loader func(ctx context.Context) (T, error),
	opts ...Option,
) (T, error) {
	var zero T
	if r == nil {
		if loader == nil {
			return zero, errNilLoader
		}
		return loader(ctx)
	}

	merged := r.def
	// 调用方选项叠加在配置默认之上。TTL 的 0 表示「沿用配置」，
	// <-0 表示永不过期，两者不再靠另一个 bool 区分。
	for _, fn := range opts {
		if fn == nil {
			continue
		}
		override := merged
		fn(&override)
		if override.TTL == 0 {
			override.TTL = merged.TTL
		}
		merged = override
	}

	// Options 是值类型，这里把合并后的结果以闭包形式回填。
	return GetOrSet(ctx, r.cache, key, loader, func(o *Options) {
		*o = merged
	})
}
