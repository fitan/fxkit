package cachex

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
	"github.com/redis/go-redis/v9"
)

// Module 声明 cachex 的依赖注入定义。
// 提供 *Config、Cache 与 *Resolver（让 Config 的 TTL/抖动/单飞设置生效）。
var Module = di.Module("fxkit/cachex",
	config.Provide[Config]("cache"),
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (Cache, error) {
			cfg, err := di.Invoke[*Config](i)
			if err != nil {
				return nil, err
			}
			c, err := New(cfg)
			if err != nil {
				return nil, err
			}
			// 仅当模块自己创建了 Redis 客户端时才由 Lifecycle 负责关闭；
			// 外部注入的客户端仍由调用方管理。
			if closer, ok := c.(interface{ Close() error }); ok {
				if lc, lcErr := di.Invoke[*di.Lifecycle](i); lcErr == nil && lc != nil {
					lc.Append(di.Hook{
						OnStop: func(context.Context) error {
							return closer.Close()
						},
					})
				}
			}
			return c, nil
		})
		di.Provide(i, func(i di.Injector) (*Resolver, error) {
			cfg, _ := di.Invoke[*Config](i)
			c, err := di.Invoke[Cache](i)
			if err != nil {
				return nil, err
			}
			return NewResolver(c, cfg), nil
		})

		// 缓存健康检查：仅在使用 Redis 时注册（进程内缓存没有远端依赖）。
		if cfg, err := di.Invoke[*Config](i); err == nil && cfg != nil &&
			strings.EqualFold(strings.TrimSpace(cfg.Driver), "redis") {
			if c, err := di.Invoke[Cache](i); err == nil && c != nil {
				di.ProvideGroupItem[server.HealthChecker](i, cacheHealthChecker{cache: c})
			}
		}
	},
)

// cacheHealthChecker 实现 [server.HealthChecker]，对 Redis 做 PING。
type cacheHealthChecker struct {
	cache Cache
}

// Name 返回检查项名称。
func (cacheHealthChecker) Name() string { return "cache" }

// Check 对 Redis 做一次 PING。
//
// 按能力（Ping）而不是具体类型（*RedisCache）判定：配了 cache.prefix 时
// 缓存会被 *prefixedCache 包一层，类型断言会失败并让探测永远通过。
func (c cacheHealthChecker) Check(ctx context.Context) error {
	p, ok := c.cache.(interface{ Ping(context.Context) error })
	if !ok {
		return fmt.Errorf("cache: driver %T does not support ping", c.cache)
	}
	if err := p.Ping(ctx); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}

// New 根据 Config 实例化 Cache。
func New(cfg *Config) (Cache, error) {
	if cfg == nil {
		return &DisabledCache{}, nil
	}

	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	var c Cache

	switch driver {
	case "":
		slog.Debug("cachex: disabled (driver is empty)")
		c = &DisabledCache{}
	case "memory":
		slog.Info("cachex: initialized in-memory driver")
		c = NewMemoryCache()
	case "redis":
		rdb := redis.NewClient(&redis.Options{
			Addr:         cfg.Redis.Addr,
			Password:     cfg.Redis.Password,
			DB:           cfg.Redis.DB,
			PoolSize:     cfg.Redis.PoolSize,
			MinIdleConns: cfg.Redis.MinIdleConns,
			DialTimeout:  cfg.Redis.DialTimeout,
			ReadTimeout:  cfg.Redis.ReadTimeout,
			WriteTimeout: cfg.Redis.WriteTimeout,
		})
		slog.Info("cachex: initialized redis driver", "addr", cfg.Redis.Addr, "db", cfg.Redis.DB)
		c = NewRedisCache(rdb)
	default:
		return nil, fmt.Errorf("cache: unsupported driver %q", cfg.Driver)
	}

	// 若配置了前缀，自动包装
	if cfg.Prefix != "" && driver != "" {
		c = wrapPrefix(cfg.Prefix, c)
	}

	return c, nil
}
