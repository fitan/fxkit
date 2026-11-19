// Package idempotency 提供 opt-in 的 HTTP 幂等键支持。
//
// 只对带 Idempotency-Key 的 mutating 请求生效；默认用 DB 存储请求 hash 与响应。
package idempotency

import (
	"fmt"
	"strings"
	"time"
)

// Config 是 idempotency 段配置。
type Config struct {
	// Enabled 为 true 时才启用幂等键中间件。
	Enabled bool `yaml:"enabled"`
	// Header 是携带幂等键的请求头名，默认 Idempotency-Key；必填。
	Header string `yaml:"header"`
	// TTL 是幂等记录保留时长，默认 24h；必须 > 0。
	TTL time.Duration `yaml:"ttl"`
	// Methods 是启用幂等的 HTTP 方法，默认 POST/PUT/PATCH/DELETE；不能为空。
	Methods []string `yaml:"methods"`
	// MaxBodyBytes 是可缓存的请求体上限（字节），默认 1 MiB；超过则返回 400。
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
	// Store 是存储后端，目前仅支持 "db"（默认）。
	Store string `yaml:"store"`
	// PurgeInterval 过期 key 清理间隔；0 = 关闭。默认 1h。
	PurgeInterval time.Duration `yaml:"purge_interval"`
}

// SetDefaults 填入默认值：Idempotency-Key 头、24h TTL、POST/PUT/PATCH/DELETE、1 MiB 上限、db 存储、1h 清理。
func (c *Config) SetDefaults() {
	c.Header = "Idempotency-Key"
	c.TTL = 24 * time.Hour
	c.Methods = []string{"POST", "PUT", "PATCH", "DELETE"}
	c.MaxBodyBytes = 1 << 20
	c.Store = "db"
	c.PurgeInterval = time.Hour
}

// Validate 校验必填项与取值范围：header、ttl、methods、max_body_bytes、store=db、purge_interval>=0。
func (c Config) Validate() error {
	if strings.TrimSpace(c.Header) == "" {
		return fmt.Errorf("idempotency.header is required")
	}
	if c.TTL <= 0 {
		return fmt.Errorf("idempotency.ttl must be > 0")
	}
	if len(c.Methods) == 0 {
		return fmt.Errorf("idempotency.methods is required")
	}
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("idempotency.max_body_bytes must be > 0")
	}
	if c.Store != "db" {
		return fmt.Errorf("idempotency.store %q is not supported (use db)", c.Store)
	}
	if c.PurgeInterval < 0 {
		return fmt.Errorf("idempotency.purge_interval must be >= 0")
	}
	return nil
}
