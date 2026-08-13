// Package config 提供框架级配置：强类型 [Core] 含各 fxkit 子系统配置段（server、app、db、otel、auth、discovery），
// 以及 Viper 驱动的 [Config]；宿主应用通过 [Config.UnmarshalKey] 或 [Config.Viper] 解析自有配置段。
//
// 结构体字段使用 `yaml` tag（与 configs/*.yaml 一致）。Viper 内部仍经 mapstructure 解码，
// 但 [yamlDecoder] 将 TagName 设为 "yaml"，因此无需再写 mapstructure tag。
//
// 优先级（高到低）：
//
//	1. 通过 [Options] 传入的 cobra flag 覆盖（如 --port）
//	2. FXKIT_ 前缀环境变量（例如 FXKIT_SERVER_PORT）
//	3. Consul KV YAML（Options.ConsulAddress + ConsulConfigKey；覆盖本地文件同名键）
//	4. Options.ConfigFile 指定的本地 YAML 文件
//	5. 编译期默认值（见 defaults.go）
//
// 启动时可用 CLI `--consul` / `--consul-key`（或环境变量 FXKIT_CONFIG_CONSUL /
// FXKIT_CONFIG_CONSUL_KEY）直接从 Consul KV 拉配置，无需本地文件。
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

// yamlDecoder tells Viper's mapstructure-backed Unmarshal to read `yaml` struct tags
// (config files are YAML; mapstructure tags are not required on app structs).
func yamlDecoder(c *mapstructure.DecoderConfig) {
	c.TagName = "yaml"
}

// Options 将 cobra 提供的覆盖项传入配置构建器。
//
// 在 main 包中通过 [fx.Supply] 填充（[cli] 包会根据 --config / --consul / --port flag 自动完成）。
type Options struct {
	ConfigFile      string // 本地 YAML 文件；为空时忽略
	ConsulAddress   string // Consul HTTP 地址（如 localhost:8500）；非空则从 KV 拉配置
	ConsulConfigKey string // Consul KV key（YAML 全文）；与 ConsulAddress 成对使用
	Port            string // 非空时覆盖 server.port
	EnvPrefix       string // 覆盖默认 FXKIT 环境变量前缀
}

// Core 持有框架配置段。宿主应用自有配置不应加在此处；请用 [Config.UnmarshalKey] 加载到自有 struct。
type Core struct {
	Server    ServerConfig    `yaml:"server"`
	App       AppMeta         `yaml:"app"`
	DB        DBConfig        `yaml:"db"`
	Outbox    OutboxConfig    `yaml:"outbox"`
	Hatchet   HatchetConfig   `yaml:"hatchet"`
	Otel      OtelConfig      `yaml:"otel"`
	Auth      AuthConfig      `yaml:"auth"`
	Discovery DiscoveryConfig `yaml:"discovery"`
}

// ServerConfig 配置内嵌 HTTP 服务器。
type ServerConfig struct {
	Port               string   `yaml:"port"`
	LogPayloads        bool     `yaml:"log_payloads"`
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
}

// AppMeta 为应用提供可读标识（用于 OpenAPI 标题等）。
type AppMeta struct {
	Name                     string `yaml:"name"`
	SeedDemoSchedulesOnStart bool   `yaml:"seed_demo_schedules_on_start"`
}

// OutboxConfig controls the transactional outbox relay (fxkit/outbox).
type OutboxConfig struct {
	Enabled      bool   `yaml:"enabled"`
	PollInterval string `yaml:"poll_interval"` // e.g. "1s"
	BatchSize    int    `yaml:"batch_size"`
	MaxRetries   int    `yaml:"max_retries"`
	// ClaimTimeout is how long a processing lease lasts before another relay may reclaim
	// the row (crash recovery). Empty/invalid defaults to 30s.
	ClaimTimeout string `yaml:"claim_timeout"` // e.g. "30s"
}

// PollDuration parses PollInterval; defaults to 1s on empty, invalid, or non-positive values.
func (c OutboxConfig) PollDuration() time.Duration {
	if c.PollInterval == "" {
		return time.Second
	}
	d, err := time.ParseDuration(c.PollInterval)
	if err != nil || d <= 0 {
		return time.Second
	}
	return d
}

// ClaimTimeoutDuration parses ClaimTimeout; defaults to 30s on empty or invalid values.
func (c OutboxConfig) ClaimTimeoutDuration() time.Duration {
	const defaultClaim = 30 * time.Second
	if c.ClaimTimeout == "" {
		return defaultClaim
	}
	d, err := time.ParseDuration(c.ClaimTimeout)
	if err != nil || d <= 0 {
		return defaultClaim
	}
	return d
}

// HatchetConfig controls the optional Hatchet client/worker (fxkit/hatchetx).
// Token/host may also come from HATCHET_CLIENT_* env vars used by the SDK.
type HatchetConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Token      string `yaml:"token"`       // or HATCHET_CLIENT_TOKEN
	HostPort   string `yaml:"host_port"`  // e.g. "localhost:7077"
	Namespace  string `yaml:"namespace"`
	WorkerName string `yaml:"worker_name"`
	// OutboxPublisher when true makes outbox.Relay push Hatchet events.
	OutboxPublisher bool `yaml:"outbox_publisher"`
}

// DBConfig 描述单个 GORM 连接。多库配置应放在宿主应用中。
type DBConfig struct {
	Driver             string `yaml:"driver"` // mysql | postgres | sqlite
	DSN                string `yaml:"dsn"`
	LogLevel           string `yaml:"log_level"` // silent | error | warn | info
	MaxIdleConns       int    `yaml:"max_idle_conns"`
	MaxOpenConns       int    `yaml:"max_open_conns"`
	ConnMaxLifetimeSec int    `yaml:"conn_max_lifetime_sec"`
}

// OtelConfig 配置 OpenTelemetry SDK 导出器与采样策略。
type OtelConfig struct {
	Enabled        bool              `yaml:"enabled"`
	ServiceName    string            `yaml:"service_name"`
	ServiceVersion string            `yaml:"service_version"`
	Environment    string            `yaml:"environment"`
	Endpoint       string            `yaml:"endpoint"`
	Protocol       string            `yaml:"protocol"` // grpc | http — exporter 为 otlp 时的 OTLP 传输方式
	Insecure       bool              `yaml:"insecure"`
	Sampling       string            `yaml:"sampling"` // always_on | always_off | trace_id_ratio:0.5 | parent_based_always_on | parent_based_trace_id_ratio:0.5
	Traces         OtelTracesConfig  `yaml:"traces"`
	Metrics        OtelMetricsConfig `yaml:"metrics"`
	Logs           OtelLogsConfig    `yaml:"logs"`
}

// HTTPEnabled 报告 chi 是否应安装 otelhttp（trace 和/或 metrics）。
func (c OtelConfig) HTTPEnabled() bool {
	return c.Enabled && (c.Traces.Enabled || c.Metrics.Enabled)
}

type OtelTracesConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Exporter string `yaml:"exporter"` // otlp | stdout
}

type OtelMetricsConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Exporter       string `yaml:"exporter"` // otlp | stdout
	RuntimeMetrics bool   `yaml:"runtime_metrics"`
}

type OtelLogsConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Exporter string `yaml:"exporter"` // otlp | stdout
}

// AuthConfig 配置身份校验（JWT / X-User）与 Casbin 授权（fxkit/authz）。
// JWT 只负责「是谁」；当 Casbin 启用时，权限由策略表决定，不再依赖 token scopes。
type AuthConfig struct {
	Enabled bool `yaml:"enabled"`
	// Issuer 为 OIDC issuer（如 https://logto.example.com/oidc）。
	// Casbin + DevHeaderUser 本地模式可不设。
	Issuer string `yaml:"issuer"`
	// Audience 为 API Resource indicator（access token 的 aud）。
	Audience string `yaml:"audience"`
	// JWKSURL 默认 {issuer}/jwks。
	JWKSURL string `yaml:"jwks_url"`
	// OrgClaim 多租户组织 ID claim，默认 organization_id。
	OrgClaim string `yaml:"org_claim"`
	// RequireOrg 为 true 时拒绝缺少 OrgClaim 的 token。
	RequireOrg bool `yaml:"require_org"`
	// DevHeaderUser 允许用 X-User 代替 JWT（仅本地联调；生产务必 false）。
	DevHeaderUser bool `yaml:"dev_header_user"`
	// TLSInsecure 跳过 JWKS HTTPS 证书校验（仅本地自签 Logto；生产务必 false）。
	TLSInsecure bool `yaml:"tls_insecure"`
	// DenyUnregistered 为 true 时，auth.enabled 下未登记的 Huma 路由返回 403。
	// 默认 false：显式登记才保护（fail-open）。
	DenyUnregistered bool `yaml:"deny_unregistered"`
	// Casbin 为 path/method RBAC（Postgres 持久化）。启用后登记路由走 Enforce(sub, path, METHOD)。
	Casbin CasbinConfig `yaml:"casbin"`
}

// CasbinConfig 配置 fxkit/authz Casbin enforcer。
type CasbinConfig struct {
	Enabled bool `yaml:"enabled"`
	// TableName 为策略表名，默认 casbin_rule。
	TableName string `yaml:"table_name"`
	// AutoRegisterRoutes 启动时把 [authz.HTTPRoute] 登记为 Casbin p 规则（赋给 BootstrapRole）。
	// 默认 true：业务只 ProvideHTTPRoutes，无需手写 seed。
	AutoRegisterRoutes bool `yaml:"auto_register_routes"`
	// BootstrapRole 自动注册权限时的目标角色，默认 admin。
	BootstrapRole string `yaml:"bootstrap_role"`
	// BootstrapUsers 启动时绑定到 BootstrapRole 的用户（如本地 alice）；可空。
	BootstrapUsers []string `yaml:"bootstrap_users"`
	// AutoBindBootstrap 为 true 时，已认证但尚无角色的 subject 首次请求会自动绑到 BootstrapRole。
	// 便于本地 Logto 联调（JWT sub ≠ alice）；生产务必 false。
	AutoBindBootstrap bool `yaml:"auto_bind_bootstrap"`
}

// DiscoveryConfig 由 consulx / reqx 消费。
type DiscoveryConfig struct {
	ConsulAddress     string `yaml:"consul_address"`
	ConsulPassingOnly bool   `yaml:"consul_passing_only"`
	// Register 为 true 时由 consulx 自注册 app.name（TTL 心跳），停机注销。
	// 无 sidecar（如 Dapr）代注册时打开；默认 false，仅做元数据补丁。
	Register bool `yaml:"register"`
	// AdvertiseAddress 写入 Consul catalog 的地址（reqx 拨号目标）。
	// 空则自动探测本机非 VPN IPv4；也可用环境变量 FXKIT_ADVERTISE_ADDRESS 覆盖。
	AdvertiseAddress string `yaml:"advertise_address"`
}

// Config 是运行时配置句柄。并发安全，提供类型化快照（[Get]）与底层 [viper.Viper] 供宿主解析。
type Config struct {
	mu   sync.RWMutex
	v    *viper.Viper
	data Core
	opts Options
}

// Get 返回已解析 [Core] 配置的不可变快照。
func (c *Config) Get() Core {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data
}

// Viper 返回底层 viper 实例。用于加载不属于 [Core] 的自有配置段。
//
// 示例：
//
//	var my MyAppConfig
//	if err := cfg.UnmarshalKey("myapp", &my); err != nil { ... }
func (c *Config) Viper() *viper.Viper { return c.v }

// UnmarshalKey 将 key 处的值解码到 out。使用 `yaml` struct tag（与配置文件格式一致）。
func (c *Config) UnmarshalKey(key string, out any) error {
	return c.v.UnmarshalKey(key, out, yamlDecoder)
}

// Sync 从当前 viper 状态（flag、env、Set）重新解析 [Core]。在无配置文件 reload 需求、仅程序化更新 viper 后使用。
func (c *Config) Sync() error {
	return c.unmarshal()
}

// Reload 重新读取本地 YAML（若有）与 Consul KV（若有），并刷新 [Core] 快照。
// [Options] 经 CLI 提供的覆盖在 reload 后保留。
func (c *Config) Reload() error {
	if err := c.loadSources(context.Background()); err != nil {
		return fmt.Errorf("config reload: %w", err)
	}
	if c.opts.Port != "" {
		c.v.Set("server.port", c.opts.Port)
	}
	return c.unmarshal()
}

func (c *Config) unmarshal() error {
	var data Core
	if err := c.v.Unmarshal(&data, yamlDecoder); err != nil {
		return err
	}
	c.mu.Lock()
	c.data = data
	c.mu.Unlock()
	return nil
}

// New 构建 [Config]，遵循 CLI > env > consul/file > defaults 优先级。
//
// Module 会自动装配该构造函数；仅在测试或 Fx 外引导时直接调用 New。
func New(opts Options) (*Config, error) {
	opts = applyConfigBootstrapEnv(opts)

	v := viper.New()
	v.SetConfigType("yaml")

	prefix := opts.EnvPrefix
	if prefix == "" {
		prefix = "FXKIT"
	}
	v.SetEnvPrefix(prefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	cfg := &Config{v: v, opts: opts}
	if err := cfg.loadSources(context.Background()); err != nil {
		return nil, err
	}

	if opts.Port != "" {
		v.Set("server.port", opts.Port)
	}

	if err := cfg.unmarshal(); err != nil {
		return nil, fmt.Errorf("config unmarshal: %w", err)
	}

	// Bootstrap --consul also seeds discovery when YAML omitted the address.
	if addr := strings.TrimSpace(opts.ConsulAddress); addr != "" &&
		strings.TrimSpace(cfg.data.Discovery.ConsulAddress) == "" {
		v.Set("discovery.consul_address", addr)
		if err := cfg.unmarshal(); err != nil {
			return nil, fmt.Errorf("config unmarshal: %w", err)
		}
	}

	slog.Info("config resolved",
		"server.port", cfg.data.Server.Port,
		"app.name", cfg.data.App.Name,
		"db.driver", cfg.data.DB.Driver,
		"otel.enabled", cfg.data.Otel.Enabled,
		"consul_config", strings.TrimSpace(opts.ConsulAddress) != "",
	)
	return cfg, nil
}

const (
	envConfigConsul    = "FXKIT_CONFIG_CONSUL"
	envConfigConsulKey = "FXKIT_CONFIG_CONSUL_KEY"
)

// applyConfigBootstrapEnv fills Consul bootstrap fields from env when CLI left them empty.
func applyConfigBootstrapEnv(opts Options) Options {
	if strings.TrimSpace(opts.ConsulAddress) == "" {
		opts.ConsulAddress = strings.TrimSpace(os.Getenv(envConfigConsul))
	}
	if strings.TrimSpace(opts.ConsulConfigKey) == "" {
		opts.ConsulConfigKey = strings.TrimSpace(os.Getenv(envConfigConsulKey))
	}
	return opts
}

// loadSources merges local file (optional when Consul is set) then Consul KV YAML.
func (c *Config) loadSources(ctx context.Context) error {
	opts := c.opts
	consulAddr := strings.TrimSpace(opts.ConsulAddress)
	consulKey := strings.TrimSpace(opts.ConsulConfigKey)

	if consulAddr != "" && consulKey == "" {
		return fmt.Errorf("config: --consul-key (or %s) is required when Consul address is set", envConfigConsulKey)
	}
	if consulKey != "" && consulAddr == "" {
		return fmt.Errorf("config: --consul (or %s) is required when Consul config key is set", envConfigConsul)
	}

	if opts.ConfigFile != "" {
		c.v.SetConfigFile(opts.ConfigFile)
		if err := c.v.ReadInConfig(); err != nil {
			// Soft-skip missing local file only when Consul is the primary remote source.
			if consulAddr != "" && isConfigFileNotFound(err) {
				slog.Warn("local config file missing; loading from Consul only",
					"path", opts.ConfigFile,
				)
			} else {
				return fmt.Errorf("config file %s: %w", opts.ConfigFile, err)
			}
		} else {
			slog.Info("config file loaded", "path", opts.ConfigFile)
		}
	}

	if consulAddr == "" {
		return nil
	}

	raw, err := fetchConsulKV(ctx, fetchConsulKVInput{
		Address: consulAddr,
		Key:     consulKey,
	})
	if err != nil {
		return fmt.Errorf("config consul %s key %q: %w", consulAddr, consulKey, err)
	}
	if err := c.v.MergeConfig(bytes.NewReader(raw)); err != nil {
		return fmt.Errorf("config consul merge %q: %w", consulKey, err)
	}
	slog.Info("config consul loaded", "address", consulAddr, "key", consulKey)
	return nil
}

func isConfigFileNotFound(err error) bool {
	if err == nil {
		return false
	}
	var notFound viper.ConfigFileNotFoundError
	if errors.As(err, &notFound) {
		return true
	}
	return os.IsNotExist(err) || errors.Is(err, os.ErrNotExist)
}

// Module 为 Fx 装配 [Config]。期望已 supply [Options]（[cli] 包从 Cobra flag 提供）。
var Module = fx.Module("fxkit/config",
	fx.Provide(New),
)
