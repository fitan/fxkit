package gormx

import (
	"fmt"
	"strings"
	"time"
)

// Config 单个 GORM 连接（yaml: db）。driver 与 dsn 都空则不连库。
type Config struct {
	// Driver：mysql | postgres | sqlite。空 = 不连库。
	Driver string `yaml:"driver"`
	// DSN 连接串。空 = 不连库。
	DSN string `yaml:"dsn"`
	// LogLevel：silent | error | warn | info。默认 warn（避免把全量 SQL 打进生产日志）。
	LogLevel string `yaml:"log_level"`
	// SlowThreshold 慢查询告警阈值，默认 200ms。0 = 关闭慢查询日志。
	SlowThreshold time.Duration `yaml:"slow_threshold"`
	// MaxIdleConns 空闲连接上限，默认 10。
	MaxIdleConns int `yaml:"max_idle_conns"`
	// MaxOpenConns 最大打开连接，默认 100。
	MaxOpenConns int `yaml:"max_open_conns"`
	// ConnMaxLifetime 连接最长存活时间，默认 1h。0 = 不限制。
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
}

// SetDefaults 填入编译期默认值。
func (c *Config) SetDefaults() {
	c.LogLevel = "warn"
	c.SlowThreshold = 200 * time.Millisecond
	c.MaxIdleConns = 10
	c.MaxOpenConns = 100
	c.ConnMaxLifetime = time.Hour
}

// Validate 检查 driver/dsn 配对与枚举。非法值启动失败。
func (c Config) Validate() error {
	driver := strings.TrimSpace(c.Driver)
	dsn := strings.TrimSpace(c.DSN)
	if (driver == "") != (dsn == "") {
		return fmt.Errorf("db.driver and db.dsn must both be set or both be empty")
	}
	if driver != "" {
		switch strings.ToLower(driver) {
		case "mysql", "postgres", "sqlite":
		default:
			return fmt.Errorf("db.driver %q is not mysql, postgres, or sqlite", c.Driver)
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.LogLevel)) {
	case "silent", "error", "warn", "info":
	default:
		return fmt.Errorf("db.log_level %q is not silent, error, warn, or info", c.LogLevel)
	}
	if c.SlowThreshold < 0 {
		return fmt.Errorf("db.slow_threshold must be >= 0")
	}
	if c.MaxIdleConns < 0 {
		return fmt.Errorf("db.max_idle_conns must be >= 0")
	}
	if c.MaxOpenConns < 0 {
		return fmt.Errorf("db.max_open_conns must be >= 0")
	}
	if c.ConnMaxLifetime < 0 {
		return fmt.Errorf("db.conn_max_lifetime must be >= 0")
	}
	return nil
}
