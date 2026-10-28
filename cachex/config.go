package cachex

import (
	"fmt"
	"strings"
	"time"
)

// Config 缓存模块配置（yaml: cache）。
type Config struct {
	// Driver 缓存驱动类型：""（默认/禁用）、"memory"（纯内存缓存）、"redis"（Redis 分布式缓存）。
	Driver string `yaml:"driver"`

	// Prefix 缓存键统一命名前缀（例如 "app:catalog:"），避免多服务或多环境键名冲突。
	Prefix string `yaml:"prefix"`

	// DefaultTTL 默认过期时间。如果调用端未指定具体 TTL 则采用此值。默认 30m。
	DefaultTTL time.Duration `yaml:"default_ttl"`

	// NullTTL 针对空结果（如记录未找到）的缓存时间，用于防缓存穿透。0 表示不缓存空值。默认 1m。
	NullTTL time.Duration `yaml:"null_ttl"`

	// Singleflight 是否启用 singleflight 合并并发加载请求，用于防缓存击穿。默认 true。
	Singleflight bool `yaml:"singleflight"`

	// JitterMaxPercent 过期时间抖动最大百分比（0-50），用于防缓存雪崩。默认 10（即 ±10%）。
	JitterMaxPercent int `yaml:"jitter_max_percent"`

	// Redis 当 Driver 为 "redis" 时的连接配置。
	Redis RedisConfig `yaml:"redis"`
}

// RedisConfig Redis 详细连接配置。
type RedisConfig struct {
	// Addr Redis 服务器地址（例如 "127.0.0.1:6379"）。
	Addr string `yaml:"addr"`
	// Password 认证密码，若无请留空。
	Password string `yaml:"password"`
	// DB 数据库序号，默认 0。
	DB int `yaml:"db"`
	// PoolSize 连接池大小，默认 100。
	PoolSize int `yaml:"pool_size"`
	// MinIdleConns 最小空闲连接数，默认 10。
	MinIdleConns int `yaml:"min_idle_conns"`
	// DialTimeout 连接超时时间，默认 5s。
	DialTimeout time.Duration `yaml:"dial_timeout"`
	// ReadTimeout 读超时时间，默认 3s。
	ReadTimeout time.Duration `yaml:"read_timeout"`
	// WriteTimeout 写超时时间，默认 3s。
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

// SetDefaults 设置配置项默认值。
func (c *Config) SetDefaults() {
	c.DefaultTTL = 30 * time.Minute
	c.NullTTL = 1 * time.Minute
	c.Singleflight = true
	c.JitterMaxPercent = 10

	c.Redis.PoolSize = 100
	c.Redis.MinIdleConns = 10
	c.Redis.DialTimeout = 5 * time.Second
	c.Redis.ReadTimeout = 3 * time.Second
	c.Redis.WriteTimeout = 3 * time.Second
}

// Validate 校验配置合法性。
func (c Config) Validate() error {
	driver := strings.ToLower(strings.TrimSpace(c.Driver))
	if driver == "" {
		return nil
	}
	switch driver {
	case "memory":
		return nil
	case "redis":
		if strings.TrimSpace(c.Redis.Addr) == "" {
			return fmt.Errorf("cache: redis.addr is required when driver is redis")
		}
		if c.Redis.PoolSize < 0 {
			return fmt.Errorf("cache: redis.pool_size must be >= 0")
		}
		return nil
	default:
		return fmt.Errorf("cache: unknown driver %q (supported: memory, redis)", c.Driver)
	}
}
