package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultReadHeaderTimeout = 30 * time.Second
	defaultReadTimeout       = 60 * time.Second
	defaultWriteTimeout      = 120 * time.Second
	defaultIdleTimeout       = 120 * time.Second
)

var (
	defaultCORSAllowedHeaders = []string{"Content-Type", "Authorization"}
	defaultCORSAllowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	defaultCORSExposeHeaders  = []string{"Content-Type"}
)

// Config 配置内嵌 chi HTTP 服务器（yaml: server）。
type Config struct {
	// Port 监听端口，默认 8080。CLI --port 可覆盖。
	Port string `yaml:"port"`
	// LogPayloads 为 true 时记录 JSON 请求/响应体（含脱敏启发式）；生产慎开。
	LogPayloads bool `yaml:"log_payloads"`
	// CORSAllowedOrigins 允许的 Origin。空 = 不设 CORS；显式 ["*"] 才允许任意源。
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
	// CORSAllowedHeaders 预检允许的请求头。默认 Content-Type, Authorization。
	CORSAllowedHeaders []string `yaml:"cors_allowed_headers"`
	// CORSAllowedMethods 预检允许的方法。默认 GET, POST, PUT, PATCH, DELETE, OPTIONS。
	CORSAllowedMethods []string `yaml:"cors_allowed_methods"`
	// CORSExposeHeaders 暴露给前端的响应头。默认 Content-Type。
	CORSExposeHeaders []string `yaml:"cors_expose_headers"`
	// CORSAllowCredentials 设置 Access-Control-Allow-Credentials。与 ["*"] 同时开启时回显请求 Origin。
	CORSAllowCredentials bool `yaml:"cors_allow_credentials"`
	// CORSMaxAge 预检缓存时间。0 = 不设置 Max-Age。
	CORSMaxAge time.Duration `yaml:"cors_max_age"`
	// ReadHeaderTimeout 读取请求头超时，默认 30s。
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	// ReadTimeout 读取完整请求超时，默认 60s。
	ReadTimeout time.Duration `yaml:"read_timeout"`
	// WriteTimeout 写响应超时，默认 120s。SSE 连接会在中间件里清掉写截止。
	WriteTimeout time.Duration `yaml:"write_timeout"`
	// IdleTimeout keep-alive 空闲超时，默认 120s。
	IdleTimeout time.Duration `yaml:"idle_timeout"`
}

// SetDefaults 填入编译期默认值。
func (c *Config) SetDefaults() {
	c.Port = "8080"
	c.LogPayloads = false
	c.CORSAllowedOrigins = []string{}
	c.CORSAllowedHeaders = append([]string(nil), defaultCORSAllowedHeaders...)
	c.CORSAllowedMethods = append([]string(nil), defaultCORSAllowedMethods...)
	c.CORSExposeHeaders = append([]string(nil), defaultCORSExposeHeaders...)
	c.ReadHeaderTimeout = defaultReadHeaderTimeout
	c.ReadTimeout = defaultReadTimeout
	c.WriteTimeout = defaultWriteTimeout
	c.IdleTimeout = defaultIdleTimeout
}

// Validate 检查端口与超时。非法值启动失败。
func (c Config) Validate() error {
	p := strings.TrimSpace(c.Port)
	if p == "" {
		return fmt.Errorf("server.port is required")
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("server.port %q is not a valid TCP port", c.Port)
	}
	if c.ReadHeaderTimeout < 0 {
		return fmt.Errorf("server.read_header_timeout must be >= 0")
	}
	if c.ReadTimeout < 0 {
		return fmt.Errorf("server.read_timeout must be >= 0")
	}
	if c.WriteTimeout < 0 {
		return fmt.Errorf("server.write_timeout must be >= 0")
	}
	if c.IdleTimeout < 0 {
		return fmt.Errorf("server.idle_timeout must be >= 0")
	}
	if c.CORSMaxAge < 0 {
		return fmt.Errorf("server.cors_max_age must be >= 0")
	}
	return nil
}

func durationOr(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}
