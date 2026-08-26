package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type yamlServer struct {
	Port               string   `yaml:"port"`
	LogPayloads        bool     `yaml:"log_payloads"`
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
}

type yamlDB struct {
	Driver          string        `yaml:"driver"`
	DSN             string        `yaml:"dsn"`
	LogLevel        string        `yaml:"log_level"`
	MaxIdleConns    int           `yaml:"max_idle_conns"`
	MaxOpenConns    int           `yaml:"max_open_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
}

type yamlOutbox struct {
	Enabled      bool          `yaml:"enabled"`
	PollInterval time.Duration `yaml:"poll_interval"`
	BatchSize    int           `yaml:"batch_size"`
	MaxRetries   int           `yaml:"max_retries"`
	ClaimTimeout time.Duration `yaml:"claim_timeout"`
}

type yamlOtel struct {
	Enabled  bool   `yaml:"enabled"`
	Protocol string `yaml:"protocol"`
	Insecure bool   `yaml:"insecure"`
	Traces   struct {
		Enabled  bool   `yaml:"enabled"`
		Exporter string `yaml:"exporter"`
	} `yaml:"traces"`
	Metrics struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"metrics"`
}

type yamlAuth struct {
	Enabled       bool   `yaml:"enabled"`
	Issuer        string `yaml:"issuer"`
	Audience      string `yaml:"audience"`
	JWKSURL       string `yaml:"jwks_url"`
	OrgClaim      string `yaml:"org_claim"`
	RequireOrg    bool   `yaml:"require_org"`
	DevHeaderUser bool   `yaml:"dev_header_user"`
}

type yamlDiscovery struct {
	ConsulAddress     string `yaml:"consul_address"`
	ConsulPassingOnly bool   `yaml:"consul_passing_only"`
}

func TestNew_LoadsYAMLTags(t *testing.T) {
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
db:
  driver: "sqlite"
  dsn: "file:test.db"
  log_level: "warn"
  max_idle_conns: 3
  max_open_conns: 8
  conn_max_lifetime: 120s
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

	var srv yamlServer
	if err := cfg.UnmarshalKey("server", &srv); err != nil {
		t.Fatal(err)
	}
	if srv.Port != "9090" || !srv.LogPayloads {
		t.Fatalf("server: %+v", srv)
	}
	if len(srv.CORSAllowedOrigins) != 1 || srv.CORSAllowedOrigins[0] != "http://localhost:3000" {
		t.Fatalf("cors: %+v", srv.CORSAllowedOrigins)
	}

	app, err := Load[App](cfg, "app")
	if err != nil {
		t.Fatal(err)
	}
	if app.Name != "yaml-tag-app" {
		t.Fatalf("app: %+v", app)
	}

	var db yamlDB
	if err := cfg.UnmarshalKey("db", &db); err != nil {
		t.Fatal(err)
	}
	if db.Driver != "sqlite" || db.MaxIdleConns != 3 || db.ConnMaxLifetime != 120*time.Second {
		t.Fatalf("db: %+v", db)
	}

	var ob yamlOutbox
	if err := cfg.UnmarshalKey("outbox", &ob); err != nil {
		t.Fatal(err)
	}
	if !ob.Enabled || ob.BatchSize != 25 || ob.ClaimTimeout != 45*time.Second || ob.PollInterval != 2*time.Second {
		t.Fatalf("outbox: %+v", ob)
	}

	var otel yamlOtel
	if err := cfg.UnmarshalKey("otel", &otel); err != nil {
		t.Fatal(err)
	}
	if !otel.Enabled || otel.Protocol != "http" || otel.Insecure || !otel.Traces.Enabled || otel.Metrics.Enabled {
		t.Fatalf("otel: %+v", otel)
	}

	var auth yamlAuth
	if err := cfg.UnmarshalKey("auth", &auth); err != nil {
		t.Fatal(err)
	}
	if !auth.Enabled || auth.Issuer != "http://localhost:3001/oidc" || auth.Audience != "http://localhost:8081/api" ||
		auth.JWKSURL != "http://localhost:3001/oidc/jwks" || auth.OrgClaim != "organization_id" || !auth.RequireOrg || auth.DevHeaderUser {
		t.Fatalf("auth: %+v", auth)
	}

	var disc yamlDiscovery
	if err := cfg.UnmarshalKey("discovery", &disc); err != nil {
		t.Fatal(err)
	}
	if disc.ConsulAddress != "127.0.0.1:8500" || !disc.ConsulPassingOnly {
		t.Fatalf("discovery: %+v", disc)
	}
}

func TestNew_IgnoresFXKITEnvOverlay(t *testing.T) {
	t.Setenv("FXKIT_SERVER_PORT", "6553")
	cfg, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var srv yamlServer
	if err := cfg.UnmarshalKey("server", &srv); err != nil {
		t.Fatal(err)
	}
	if srv.Port != "" {
		t.Fatalf("env must not overlay config tree, port=%q", srv.Port)
	}
}

func TestNew_PortFlagWins(t *testing.T) {
	cfg, err := New(Options{Port: "7777"})
	if err != nil {
		t.Fatal(err)
	}
	var srv yamlServer
	if err := cfg.UnmarshalKey("server", &srv); err != nil {
		t.Fatal(err)
	}
	if srv.Port != "7777" {
		t.Fatalf("port=%q", srv.Port)
	}
}

func TestSet_UpdatesTree(t *testing.T) {
	cfg, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("server.port", "9090"); err != nil {
		t.Fatal(err)
	}
	var srv yamlServer
	if err := cfg.UnmarshalKey("server", &srv); err != nil {
		t.Fatal(err)
	}
	if srv.Port != "9090" {
		t.Fatalf("port=%q", srv.Port)
	}
}

func TestSet_DoesNotValidate(t *testing.T) {
	cfg, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("server.port", "not-a-port"); err != nil {
		t.Fatal(err)
	}
	var srv yamlServer
	if err := cfg.UnmarshalKey("server", &srv); err != nil {
		t.Fatal(err)
	}
	if srv.Port != "not-a-port" {
		t.Fatalf("Set should keep invalid value, got %q", srv.Port)
	}
}

func TestReload_RereadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: \"8081\"\napp:\n  name: first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := New(Options{ConfigFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("server:\n  port: \"9099\"\napp:\n  name: second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Reload(); err != nil {
		t.Fatal(err)
	}
	var srv yamlServer
	if err := cfg.UnmarshalKey("server", &srv); err != nil {
		t.Fatal(err)
	}
	app, err := Load[App](cfg, "app")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Port != "9099" || app.Name != "second" {
		t.Fatalf("after reload: %+v / %+v", srv, app)
	}
}

func TestUnmarshalKey_UsesYAMLTags(t *testing.T) {
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

type hookCfg struct {
	N int `yaml:"n"`
}

func (h *hookCfg) SetDefaults() { h.N = 7 }

func (h hookCfg) Validate() error {
	if h.N < 0 {
		return errNegativeN
	}
	return nil
}

var errNegativeN = errString("n negative")

type errString string

func (e errString) Error() string { return string(e) }

func TestLoad_TypedSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
orders:
  page_size: 20
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
	type ordersCfg struct {
		PageSize int `yaml:"page_size"`
		Feature  struct {
			Enabled bool   `yaml:"enabled"`
			Key     string `yaml:"key"`
		} `yaml:"feature"`
	}
	got, err := Load[ordersCfg](cfg, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if got.PageSize != 20 || !got.Feature.Enabled || got.Feature.Key != "abc" {
		t.Fatalf("got %+v", got)
	}
	missing, err := Load[ordersCfg](cfg, "no-such-section")
	if err != nil {
		t.Fatal(err)
	}
	if missing.PageSize != 0 {
		t.Fatalf("missing key should be zero value, got %+v", missing)
	}
}

func TestLoad_SetDefaultsAndValidate(t *testing.T) {
	cfg, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load[hookCfg](cfg, "hooks")
	if err != nil {
		t.Fatal(err)
	}
	if got.N != 7 {
		t.Fatalf("defaults: n=%d", got.N)
	}
	if err := cfg.Set("hooks.n", -1); err != nil {
		t.Fatal(err)
	}
	_, err = Load[hookCfg](cfg, "hooks")
	if err == nil || !strings.Contains(err.Error(), "n negative") {
		t.Fatalf("want validate error, got %v", err)
	}
}

func TestLoad_InvalidDuration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("outbox:\n  poll_interval: nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := New(Options{ConfigFile: path})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load[yamlOutbox](cfg, "outbox")
	if err == nil || !strings.Contains(err.Error(), "duration") {
		t.Fatalf("want duration error, got %v", err)
	}
}
