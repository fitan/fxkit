package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	"github.com/fitan/fxkit/otelx"
	"github.com/fitan/fxkit/outbox"
	"github.com/fitan/fxkit/server"
)

func TestLoad_DefaultsMatchComponents(t *testing.T) {
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}

	srv, err := config.Load[server.Config](cfg, "server")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Port != "8080" {
		t.Fatalf("port=%q", srv.Port)
	}
	if len(srv.CORSAllowedOrigins) != 0 {
		t.Fatalf("cors default must be empty, got %v", srv.CORSAllowedOrigins)
	}

	app, err := config.Load[config.App](cfg, "app")
	if err != nil {
		t.Fatal(err)
	}
	if app.Name != "fxkit-app" {
		t.Fatalf("app.name=%q", app.Name)
	}

	h, err := config.Load[hatchetx.Config](cfg, "hatchet")
	if err != nil {
		t.Fatal(err)
	}
	if h.HostPort != "localhost:7077" || !h.OutboxPublisher || !h.OTel {
		t.Fatalf("hatchet: %+v", h)
	}

	d, err := config.Load[consulx.Config](cfg, "discovery")
	if err != nil {
		t.Fatal(err)
	}
	if !d.ConsulPassingOnly || d.Register {
		t.Fatalf("discovery: %+v", d)
	}

	a, err := config.Load[authz.Config](cfg, "auth")
	if err != nil {
		t.Fatal(err)
	}
	if a.Casbin.ReloadInterval != 5*time.Second {
		t.Fatalf("casbin.reload_interval=%v", a.Casbin.ReloadInterval)
	}

	o, err := config.Load[outbox.Config](cfg, "outbox")
	if err != nil {
		t.Fatal(err)
	}
	if o.PollInterval != time.Second || o.ClaimTimeout != 30*time.Second {
		t.Fatalf("outbox: %+v", o)
	}

	db, err := config.Load[gormx.Config](cfg, "db")
	if err != nil {
		t.Fatal(err)
	}
	if db.ConnMaxLifetime != time.Hour {
		t.Fatalf("db.conn_max_lifetime=%v", db.ConnMaxLifetime)
	}
	if db.LogLevel != "warn" {
		t.Fatalf("db.log_level=%q", db.LogLevel)
	}
}

func TestLoad_IgnoresFXKITEnvOverlay(t *testing.T) {
	t.Setenv("FXKIT_SERVER_PORT", "6553")
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := config.Load[server.Config](cfg, "server")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Port != "8080" {
		t.Fatalf("env must not overlay config tree, port=%q", srv.Port)
	}
}

func TestLoad_PortFlagWins(t *testing.T) {
	cfg, err := config.New(config.Options{Port: "7777"})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := config.Load[server.Config](cfg, "server")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Port != "7777" {
		t.Fatalf("port=%q", srv.Port)
	}
}

func TestLoad_RejectsInvalidDriver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("db:\n  driver: oracle\n  dsn: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.New(config.Options{ConfigFile: path})
	if err != nil {
		t.Fatal(err)
	}
	_, err = config.Load[gormx.Config](cfg, "db")
	if err == nil || !strings.Contains(err.Error(), "db.driver") {
		t.Fatalf("want driver error, got %v", err)
	}
}

func TestLoad_RejectsDriverWithoutDSN(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("db:\n  driver: sqlite\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.New(config.Options{ConfigFile: path})
	if err != nil {
		t.Fatal(err)
	}
	_, err = config.Load[gormx.Config](cfg, "db")
	if err == nil || !strings.Contains(err.Error(), "db.dsn") {
		t.Fatalf("want driver/dsn pairing error, got %v", err)
	}
}

func TestLoad_CasbinReloadZeroDisables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("auth:\n  casbin:\n    reload_interval: 0s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.New(config.Options{ConfigFile: path})
	if err != nil {
		t.Fatal(err)
	}
	a, err := config.Load[authz.Config](cfg, "auth")
	if err != nil {
		t.Fatal(err)
	}
	if a.Casbin.ReloadInterval != 0 {
		t.Fatalf("got %v", a.Casbin.ReloadInterval)
	}
}

func TestLoad_RejectsInvalidPort(t *testing.T) {
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("server.port", "not-a-port"); err != nil {
		t.Fatal(err)
	}
	_, err = config.Load[server.Config](cfg, "server")
	if err == nil || !strings.Contains(err.Error(), "server.port") {
		t.Fatalf("want port error, got %v", err)
	}
}

func TestLoad_OtelSection(t *testing.T) {
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	o, err := config.Load[otelx.Config](cfg, "otel")
	if err != nil {
		t.Fatal(err)
	}
	if o.Enabled {
		t.Fatal("otel default enabled")
	}
	if o.Protocol != "grpc" || o.Endpoint != "localhost:4317" {
		t.Fatalf("otel defaults: %+v", o)
	}
}
