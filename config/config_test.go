package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func clearFXKITEnv(t *testing.T) {
	t.Helper()
	for _, e := range os.Environ() {
		key, _, ok := strings.Cut(e, "=")
		if !ok || !strings.HasPrefix(key, "FXKIT_") {
			continue
		}
		orig, had := os.LookupEnv(key)
		_ = os.Unsetenv(key)
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(key, orig)
			}
		})
	}
}

func TestOutboxConfigClaimTimeoutDuration(t *testing.T) {
	tests := []struct {
		name string
		cfg  OutboxConfig
		want time.Duration
	}{
		{name: "empty", cfg: OutboxConfig{}, want: 30 * time.Second},
		{name: "valid", cfg: OutboxConfig{ClaimTimeout: "45s"}, want: 45 * time.Second},
		{name: "invalid", cfg: OutboxConfig{ClaimTimeout: "nope"}, want: 30 * time.Second},
		{name: "zero", cfg: OutboxConfig{ClaimTimeout: "0s"}, want: 30 * time.Second},
		{name: "negative", cfg: OutboxConfig{ClaimTimeout: "-1s"}, want: 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ClaimTimeoutDuration(); got != tt.want {
				t.Fatalf("ClaimTimeoutDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNew_LoadsYAMLTags(t *testing.T) {
	clearFXKITEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
server:
  port: "9090"
  log_payloads: true
  cors_allowed_origins:
    - "http://localhost:3000"
app:
  name: "yaml-tag-app"
  seed_demo_schedules_on_start: false
db:
  driver: "sqlite"
  dsn: "file:test.db"
  log_level: "warn"
  max_idle_conns: 3
  max_open_conns: 8
  conn_max_lifetime_sec: 120
outbox:
  enabled: true
  poll_interval: 2s
  batch_size: 25
  max_retries: 3
  claim_timeout: 45s
otel:
  enabled: true
  service_name: "svc"
  protocol: "http"
  insecure: false
  traces:
    enabled: true
    exporter: "stdout"
  metrics:
    enabled: false
    exporter: "otlp"
    runtime_metrics: false
  logs:
    enabled: true
    exporter: "stdout"
auth:
  enabled: true
  issuer: "http://localhost:3001/oidc"
  audience: "http://localhost:8081/api"
  jwks_url: "http://localhost:3001/oidc/jwks"
  org_claim: "organization_id"
  require_org: true
  dev_header_user: false
discovery:
  consul_address: "127.0.0.1:8500"
  consul_passing_only: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := New(Options{ConfigFile: path})
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Get()
	if c.Server.Port != "9090" || !c.Server.LogPayloads {
		t.Fatalf("server: %+v", c.Server)
	}
	if len(c.Server.CORSAllowedOrigins) != 1 || c.Server.CORSAllowedOrigins[0] != "http://localhost:3000" {
		t.Fatalf("cors: %+v", c.Server.CORSAllowedOrigins)
	}
	if c.App.Name != "yaml-tag-app" || c.App.SeedDemoSchedulesOnStart {
		t.Fatalf("app: %+v", c.App)
	}
	if c.DB.Driver != "sqlite" || c.DB.MaxIdleConns != 3 || c.DB.ConnMaxLifetimeSec != 120 {
		t.Fatalf("db: %+v", c.DB)
	}
	if !c.Outbox.Enabled || c.Outbox.BatchSize != 25 || c.Outbox.ClaimTimeout != "45s" {
		t.Fatalf("outbox: %+v", c.Outbox)
	}
	if !c.Otel.Enabled || c.Otel.Protocol != "http" || c.Otel.Insecure || !c.Otel.Traces.Enabled || c.Otel.Metrics.Enabled {
		t.Fatalf("otel: %+v", c.Otel)
	}
	if !c.Auth.Enabled || c.Auth.Issuer != "http://localhost:3001/oidc" || c.Auth.Audience != "http://localhost:8081/api" ||
		c.Auth.JWKSURL != "http://localhost:3001/oidc/jwks" || c.Auth.OrgClaim != "organization_id" || !c.Auth.RequireOrg || c.Auth.DevHeaderUser {
		t.Fatalf("auth: %+v", c.Auth)
	}
	if c.Discovery.ConsulAddress != "127.0.0.1:8500" || !c.Discovery.ConsulPassingOnly {
		t.Fatalf("discovery: %+v", c.Discovery)
	}
}

func TestUnmarshalKey_UsesYAMLTags(t *testing.T) {
	clearFXKITEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
myapp:
  feature:
    enabled: true
    key: "abc"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := New(Options{ConfigFile: path})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Feature struct {
			Enabled bool   `yaml:"enabled"`
			Key     string `yaml:"key"`
		} `yaml:"feature"`
	}
	if err := cfg.UnmarshalKey("myapp", &out); err != nil {
		t.Fatal(err)
	}
	if !out.Feature.Enabled || out.Feature.Key != "abc" {
		t.Fatalf("got %+v", out)
	}
}

func TestOtelConfigHTTPEnabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  OtelConfig
		want bool
	}{
		{
			name: "disabled",
			cfg:  OtelConfig{Enabled: false, Traces: OtelTracesConfig{Enabled: true}},
			want: false,
		},
		{
			name: "traces",
			cfg:  OtelConfig{Enabled: true, Traces: OtelTracesConfig{Enabled: true}},
			want: true,
		},
		{
			name: "metrics",
			cfg:  OtelConfig{Enabled: true, Metrics: OtelMetricsConfig{Enabled: true}},
			want: true,
		},
		{
			name: "logs only",
			cfg:  OtelConfig{Enabled: true, Logs: OtelLogsConfig{Enabled: true}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.HTTPEnabled(); got != tt.want {
				t.Fatalf("HTTPEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
