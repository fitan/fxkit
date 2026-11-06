package config

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fitan/fxkit/internal/consuladdr"
)

func TestApplyConsulToken(t *testing.T) {
	t.Setenv(envConsulHTTPToken, "acl-token")
	req, err := http.NewRequest(http.MethodGet, "http://consul/v1/agent/self", nil)
	if err != nil {
		t.Fatal(err)
	}
	ApplyConsulToken(req)
	if got := req.Header.Get("X-Consul-Token"); got != "acl-token" {
		t.Fatalf("token=%q", got)
	}
}

func TestNew_LoadsFromConsulKV(t *testing.T) {
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
		ConfigFile:      "", // 仅用 consul
		ConsulAddress:   srv.URL,
		ConsulConfigKey: "config/myapp.yaml",
	})
	if err != nil {
		t.Fatal(err)
	}
	app, err := Load[App](cfg, "app")
	if err != nil {
		t.Fatal(err)
	}
	var srvPort struct {
		Port string `yaml:"port"`
	}
	if err := cfg.UnmarshalKey("server", &srvPort); err != nil {
		t.Fatal(err)
	}
	if srvPort.Port != "9099" || app.Name != "from-consul" {
		t.Fatalf("port=%q app=%q", srvPort.Port, app.Name)
	}
	var disc struct {
		ConsulAddress     string `yaml:"consul_address"`
		ConsulPassingOnly bool   `yaml:"consul_passing_only"`
	}
	if err := cfg.UnmarshalKey("discovery", &disc); err != nil {
		t.Fatal(err)
	}
	// --consul 只是配置来源；discovery 保持独立。
	if disc.ConsulAddress != "" {
		t.Fatalf("discovery.consul_address = %q, want empty (not seeded from --consul)", disc.ConsulAddress)
	}
	if !disc.ConsulPassingOnly {
		t.Fatalf("discovery: %+v", disc)
	}
}

func TestNew_ConsulOverlaysLocalFile(t *testing.T) {
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	app, err := Load[App](cfg, "app")
	if err != nil {
		t.Fatal(err)
	}
	var srvPort struct {
		Port string `yaml:"port"`
	}
	if err := cfg.UnmarshalKey("server", &srvPort); err != nil {
		t.Fatal(err)
	}
	if srvPort.Port != "7070" || app.Name != "consul-app" {
		t.Fatalf("want consul overlay, got port=%q name=%q", srvPort.Port, app.Name)
	}
}

func TestNew_ConsulAllowsMissingLocalFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	var srvPort struct {
		Port string `yaml:"port"`
	}
	if err := cfg.UnmarshalKey("server", &srvPort); err != nil {
		t.Fatal(err)
	}
	if srvPort.Port != "6060" {
		t.Fatalf("port=%q", srvPort.Port)
	}
}

func TestNew_ConsulRequiresKey(t *testing.T) {
	_, err := New(Options{ConsulAddress: "localhost:8500"})
	if err == nil || !strings.Contains(err.Error(), "consul-key") {
		t.Fatalf("want consul-key error, got %v", err)
	}
}

// TestNew_DropsStaleConsulKeys 确认每次 New 都重建配置树：
// 上一轮 KV 里存在、这一轮已删除的键不能残留。
// （以前由 Config.Reload 覆盖；该 API 已移除，改为验证 New 的等价保证。）
func TestNew_DropsStaleConsulKeys(t *testing.T) {
	var body atomic.Value
	body.Store([]byte("app:\n  name: first\notel:\n  enabled: true\n"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body.Load().([]byte))
	}))
	t.Cleanup(srv.Close)

	cfg, err := New(Options{ConsulAddress: srv.URL, ConsulConfigKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := Load[App](cfg, "app")
	if err != nil {
		t.Fatal(err)
	}
	var otel struct {
		Enabled bool `yaml:"enabled"`
	}
	if err := cfg.UnmarshalKey("otel", &otel); err != nil {
		t.Fatal(err)
	}
	if app.Name != "first" || !otel.Enabled {
		t.Fatalf("first load: name=%q otel.enabled=%v", app.Name, otel.Enabled)
	}

	// KV 变了：重新 New 一棵树，旧键不得残留。
	body.Store([]byte("app:\n  name: second\n"))
	fresh, err := New(Options{ConsulAddress: srv.URL, ConsulConfigKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	app, err = Load[App](fresh, "app")
	if err != nil {
		t.Fatal(err)
	}
	otel = struct {
		Enabled bool `yaml:"enabled"`
	}{}
	if err := fresh.UnmarshalKey("otel", &otel); err != nil {
		t.Fatal(err)
	}
	if app.Name != "second" {
		t.Fatalf("name=%q", app.Name)
	}
	if otel.Enabled {
		t.Fatal("stale otel.enabled survived a fresh load")
	}
}

func TestNormalizeConsulHTTPAddr(t *testing.T) {
	got, err := consuladdr.Normalize("localhost:8500")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://localhost:8500" {
		t.Fatalf("got %q", got)
	}
}
