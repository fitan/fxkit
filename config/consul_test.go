package config

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew_LoadsFromConsulKV(t *testing.T) {
	clearFXKITEnv(t)

	const yamlBody = `
server:
  port: "9099"
app:
  name: "from-consul"
discovery:
  consul_passing_only: true
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/config/myapp.yaml" || r.URL.Query().Get("raw") != "true" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Consul-Token"); got != "secret-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(yamlBody))
	}))
	t.Cleanup(srv.Close)

	t.Setenv(envConsulHTTPToken, "secret-token")

	cfg, err := New(Options{
		ConfigFile:      "", // consul-only
		ConsulAddress:   srv.URL,
		ConsulConfigKey: "config/myapp.yaml",
	})
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Get()
	if c.Server.Port != "9099" || c.App.Name != "from-consul" {
		t.Fatalf("core: %+v", c)
	}
	// Bootstrap consul address seeds discovery when YAML omits it.
	if c.Discovery.ConsulAddress != srv.URL {
		t.Fatalf("discovery.consul_address = %q, want bootstrap %q", c.Discovery.ConsulAddress, srv.URL)
	}
	if !c.Discovery.ConsulPassingOnly {
		t.Fatalf("discovery: %+v", c.Discovery)
	}
}

func TestNew_ConsulOverlaysLocalFile(t *testing.T) {
	clearFXKITEnv(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
server:
  port: "8080"
app:
  name: "local-app"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
server:
  port: "7070"
app:
  name: "consul-app"
`))
	}))
	t.Cleanup(srv.Close)

	cfg, err := New(Options{
		ConfigFile:      path,
		ConsulAddress:   srv.URL,
		ConsulConfigKey: "app/config",
	})
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Get()
	if c.Server.Port != "7070" || c.App.Name != "consul-app" {
		t.Fatalf("want consul overlay, got %+v / %+v", c.Server, c.App)
	}
}

func TestNew_ConsulAllowsMissingLocalFile(t *testing.T) {
	clearFXKITEnv(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("server:\n  port: \"6060\"\napp:\n  name: remote\n"))
	}))
	t.Cleanup(srv.Close)

	missing := filepath.Join(t.TempDir(), "no-such.yaml")
	cfg, err := New(Options{
		ConfigFile:      missing,
		ConsulAddress:   srv.URL,
		ConsulConfigKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Get().Server.Port != "6060" {
		t.Fatalf("port=%q", cfg.Get().Server.Port)
	}
}

func TestNew_ConsulRequiresKey(t *testing.T) {
	clearFXKITEnv(t)
	_, err := New(Options{ConsulAddress: "localhost:8500"})
	if err == nil || !strings.Contains(err.Error(), "consul-key") {
		t.Fatalf("want consul-key error, got %v", err)
	}
}

func TestNormalizeConsulHTTPAddr(t *testing.T) {
	got, err := normalizeConsulHTTPAddr("localhost:8500")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://localhost:8500" {
		t.Fatalf("got %q", got)
	}
}
