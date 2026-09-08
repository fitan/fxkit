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
	// HostPort 如 localhost:7077。loopback 且未设 TLSStrategy 时默认 TLS strategy=none。默认 localhost:7077。
	HostPort string `yaml:"host_port"`
	// ServerURL 写入 HATCHET_CLIENT_SERVER_URL（REST：cron / reminder）。
	// Token JWT 里常是 http://localhost:8080；连远程引擎时必须改成实际 dashboard，例如 http://10.170.34.223:8088。
	ServerURL string `yaml:"server_url"`
	// TLSStrategy 写入 HATCHET_CLIENT_TLS_STRATEGY：none | tls | mtls。
	// 空 = 仅 loopback 默认 none；非 loopback 的明文 gRPC（hatchet-lite SERVER_GRPC_INSECURE）必须显式 none。
	TLSStrategy string `yaml:"tls_strategy"`
	// Namespace 对应 HATCHET_CLIENT_NAMESPACE。
	Namespace string `yaml:"namespace"`
	// WorkerName worker 进程名，默认 fxkit-worker。
	WorkerName string `yaml:"worker_name"`
	// OutboxPublisher 为 true 时 outbox relay 把事件推到 Hatchet。默认 true。
	OutboxPublisher bool `yaml:"outbox_publisher"`
	// OTel 为 true 时在 worker 上挂 Hatchet instrumentor（hatchet.start_step_run）。
	// 复用 otelx 的 TracerProvider，不另起 SDK、不向 Hatchet engine 再导一份。
	// 默认 true；otelx 未启用时自动跳过。
	OTel bool `yaml:"otel"`
}

// SetDefaults 填入编译期默认值。
func (c *Config) SetDefaults() {
	c.HostPort = "localhost:7077"
	c.WorkerName = "fxkit-worker"
	c.OutboxPublisher = true
	c.OTel = true
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
	switch strings.ToLower(strings.TrimSpace(c.TLSStrategy)) {
	case "", "none", "tls", "mtls":
	default:
		return fmt.Errorf("hatchet.tls_strategy %q is not none, tls, or mtls", c.TLSStrategy)
	}
	return nil
}
