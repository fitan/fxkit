package cachex

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/redis/go-redis/v9"
)

// Module 声明 cachex 的依赖注入定义。
// 提供 *Config 与 Cache。
var Module = di.Module("fxkit/cachex",
	config.Provide[Config]("cache"),
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (Cache, error) {
			cfg, err := di.Invoke[*Config](i)
			if err != nil {
				return nil, err
			}
			return New(cfg)
		})
	},
)

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
