package server

import (
	"fmt"
	"strconv"
	"strings"
)

// Config 配置内嵌 chi HTTP 服务器（yaml: server）。
type Config struct {
	// Port 监听端口，默认 8080。CLI --port 可覆盖。
	Port string `yaml:"port"`
	// LogPayloads 为 true 时记录 JSON 请求/响应体（含脱敏启发式）；生产慎开。
	LogPayloads bool `yaml:"log_payloads"`
	// CORSAllowedOrigins 允许的 Origin。空 = 不设 CORS；显式 ["*"] 才允许任意源。
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
}

// SetDefaults 填入编译期默认值。
func (c *Config) SetDefaults() {
	c.Port = "8080"
	c.LogPayloads = false
	c.CORSAllowedOrigins = []string{}
}

// Validate 检查端口。非法值启动失败。
func (c Config) Validate() error {
	p := strings.TrimSpace(c.Port)
	if p == "" {
		return fmt.Errorf("server.port is required")
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("server.port %q is not a valid TCP port", c.Port)
	}
	return nil
}
