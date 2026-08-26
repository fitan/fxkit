package hatchetx

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// Config 可选 Hatchet client/worker（yaml: hatchet）。
// Token / host 也可由 SDK 原生环境变量 HATCHET_CLIENT_* 提供；yaml 仅在 env 未设时注入。
type Config struct {
	// Enabled 为 true 时创建 client；有 Registrar 时再启 worker。默认 false。
	Enabled bool `yaml:"enabled"`
	// Token 即 HATCHET_CLIENT_TOKEN。启用时必填（yaml 或 SDK 环境变量二选一）。
	Token string `yaml:"token"`
	// HostPort 如 localhost:7077。loopback 默认 TLS strategy=none。默认 localhost:7077。
	HostPort string `yaml:"host_port"`
	// Namespace 对应 HATCHET_CLIENT_NAMESPACE。
	Namespace string `yaml:"namespace"`
	// WorkerName worker 进程名，默认 fxkit-worker。
	WorkerName string `yaml:"worker_name"`
	// OutboxPublisher 为 true 时 outbox relay 把事件推到 Hatchet。默认 true。
	OutboxPublisher bool `yaml:"outbox_publisher"`
}

// SetDefaults 填入编译期默认值。
func (c *Config) SetDefaults() {
	c.HostPort = "localhost:7077"
	c.WorkerName = "fxkit-worker"
	c.OutboxPublisher = true
}

// Validate 检查 host_port 格式；启用时必须有 token（yaml 或环境变量）。
func (c Config) Validate() error {
	hp := strings.TrimSpace(c.HostPort)
	if hp != "" {
		if _, _, err := net.SplitHostPort(hp); err != nil {
			return fmt.Errorf("hatchet.host_port: want host:port, got %q", c.HostPort)
		}
	}
	if c.Enabled && strings.TrimSpace(c.Token) == "" && strings.TrimSpace(os.Getenv("HATCHET_CLIENT_TOKEN")) == "" {
		return fmt.Errorf("hatchet.token or HATCHET_CLIENT_TOKEN is required when hatchet.enabled=true")
	}
	return nil
}
