package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultReadHeaderTimeout 是读取请求头的默认超时。
	defaultReadHeaderTimeout = 30 * time.Second
	// defaultReadTimeout 是读取完整请求的默认超时。
	defaultReadTimeout = 60 * time.Second
	// defaultWriteTimeout 是写响应的默认超时。
	defaultWriteTimeout = 120 * time.Second
	// defaultIdleTimeout 是 keep-alive 空闲连接的默认超时。
	defaultIdleTimeout = 120 * time.Second
)

var (
	// defaultCORSAllowedHeaders 是未配置 CORSAllowedHeaders 时预检允许的请求头。
	defaultCORSAllowedHeaders = []string{"Content-Type", "Authorization"}
	// defaultCORSAllowedMethods 是未配置 CORSAllowedMethods 时预检允许的方法。
	defaultCORSAllowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	// defaultCORSExposeHeaders 是未配置 CORSExposeHeaders 时暴露给浏览器的响应头。
	defaultCORSExposeHeaders = []string{"Content-Type"}
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
	// AccessLog 为 true 时输出结构化访问日志（method/route/status/duration/bytes）。
	// route 用 chi 路由模板（避免高基数），默认关闭。
	AccessLog bool `yaml:"access_log"`
	// SSEPathPrefixes 是流式端点的路径前缀，默认 ["/sse/"]。
	// 命中前缀的请求会清掉 WriteTimeout、跳过 OTel 与 body 日志。
	// 也支持 Accept/Content-Type: text/event-stream 的请求。
	SSEPathPrefixes []string `yaml:"sse_path_prefixes"`
	// MaxBodyBytes 是裸 chi 路由（[Route]）的请求体上限，默认 1 MiB。
	//
	// Huma 路由有各自的 [huma.Operation.MaxBodyBytes]（默认也是 1 MiB）；
	// 此值只作用于通过 [ProvideRoutes] 挂载的裸 handler。
	// 0 = 不限制；单条路由可用 [Route.MaxBodyBytes] 覆盖（<0 也不限制）。
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
	// Pprof 暴露 /debug/pprof/*（默认关闭）。生产只在内网/临时开启。
	Pprof PprofConfig `yaml:"pprof"`
	// SecurityHeaders 默认开启的安全响应头。
	SecurityHeaders SecurityHeadersConfig `yaml:"security_headers"`
	// CSRF 仅用于 cookie 认证场景；Bearer token API 不需要。
	CSRF CSRFConfig `yaml:"csrf"`
}

// PprofConfig 控制 net/http/pprof 端点。
type PprofConfig struct {
	// Enabled 为 true 时挂载 /debug/pprof/*；默认关闭。
	Enabled bool `yaml:"enabled"`
	// Prefix 是 pprof 路由前缀，默认 /debug/pprof/；启用时不可为空。
	Prefix string `yaml:"prefix"`
}

// SecurityHeadersConfig 控制安全响应头。
type SecurityHeadersConfig struct {
	// Enabled 为 true 时输出安全响应头；默认开启。
	Enabled bool `yaml:"enabled"`
	// HSTS 为 true 且请求走 TLS 时输出 Strict-Transport-Security。
	HSTS bool `yaml:"hsts"`
	// CSP 非空时作为 Content-Security-Policy 响应头；空则不输出。
	CSP string `yaml:"csp"`
}

// CSRFConfig 是 double-submit cookie 校验配置。
type CSRFConfig struct {
	// Enabled 为 true 时校验非安全方法（GET/HEAD/OPTIONS/TRACE 跳过）；仅 cookie 认证需要。
	Enabled bool `yaml:"enabled"`
	// CookieName 是携带 token 的 cookie 名，默认 csrf_token；启用时不可为空。
	CookieName string `yaml:"cookie_name"`
	// HeaderName 是携带 token 的请求头名，默认 X-CSRF-Token；启用时不可为空。
	HeaderName string `yaml:"header_name"`
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
	c.AccessLog = false
	c.SSEPathPrefixes = []string{"/sse/"}
	c.MaxBodyBytes = 1 << 20 // 1 MiB，与 Huma 默认一致
	c.Pprof.Prefix = "/debug/pprof/"
	c.SecurityHeaders.Enabled = true
	c.CSRF.CookieName = "csrf_token"
	c.CSRF.HeaderName = "X-CSRF-Token"
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
	if c.Pprof.Enabled && strings.TrimSpace(c.Pprof.Prefix) == "" {
		return fmt.Errorf("server.pprof.prefix is required when pprof is enabled")
	}
	if c.CSRF.Enabled {
		if strings.TrimSpace(c.CSRF.CookieName) == "" || strings.TrimSpace(c.CSRF.HeaderName) == "" {
			return fmt.Errorf("server.csrf.cookie_name/header_name are required when csrf is enabled")
		}
	}
	return nil
}

// durationOr 返回 d；d <= 0 时返回 fallback。
func durationOr(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}
