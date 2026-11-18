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
	Enabled      bool          `yaml:"enabled"`
	Header       string        `yaml:"header"`
	TTL          time.Duration `yaml:"ttl"`
	Methods      []string      `yaml:"methods"`
	MaxBodyBytes int64         `yaml:"max_body_bytes"`
	Store        string        `yaml:"store"`
	// PurgeInterval 过期 key 清理间隔；0 = 关闭。默认 1h。
	PurgeInterval time.Duration `yaml:"purge_interval"`
}

func (c *Config) SetDefaults() {
	c.Header = "Idempotency-Key"
	c.TTL = 24 * time.Hour
	c.Methods = []string{"POST", "PUT", "PATCH", "DELETE"}
	c.MaxBodyBytes = 1 << 20
	c.Store = "db"
	c.PurgeInterval = time.Hour
}

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
