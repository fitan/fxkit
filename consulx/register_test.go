package consulx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/server"
)

func TestRegisterSelf_WritesBuildinfoAndToken(t *testing.T) {
	t.Setenv("CONSUL_HTTP_TOKEN", "acl-token")

	prev := []string{buildinfo.Version, buildinfo.Commit, buildinfo.GitBranch, buildinfo.GoVersion, buildinfo.BuildTime, buildinfo.BuiltBy}
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.GitBranch = prev[0], prev[1], prev[2]
		buildinfo.GoVersion, buildinfo.BuildTime, buildinfo.BuiltBy = prev[3], prev[4], prev[5]
	})
	buildinfo.Version = "1.2.3"
	buildinfo.Commit = "deadbeef"
	buildinfo.GitBranch = "main"
	buildinfo.GoVersion = "go1.26.0"
	buildinfo.BuildTime = "2026-08-13T00:00:00Z"
	buildinfo.BuiltBy = "ci"

	var got registerServiceRequest
	var sawToken, sawPass bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Consul-Token") != "acl-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent/service/register":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/v1/agent/check/pass/"):
			sawPass = true
			sawToken = r.Header.Get("X-Consul-Token") == "acl-token"
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := &Config{
		Register:         true,
		ConsulAddress:    srv.URL,
		AdvertiseAddress: "10.0.0.9",
	}
	app := &config.App{Name: "orders"}
	srvCfg := &server.Config{Port: "8080"}

	id, err := registerSelf(context.Background(), cfg, app, srvCfg)
	if err != nil {
		t.Fatal(err)
	}
	if id != "orders-10.0.0.9-8080" {
		t.Fatalf("id=%q", id)
	}
	if got.Name != "orders" || got.Address != "10.0.0.9" || got.Port != 8080 {
		t.Fatalf("payload=%+v", got)
	}
	if got.Meta["version"] != "1.2.3" || got.Meta["build_commit"] != "deadbeef" {
		t.Fatalf("meta=%v", got.Meta)
	}
	if got.Meta["git_branch"] != "main" || got.Meta["go_version"] != "go1.26.0" {
		t.Fatalf("meta=%v", got.Meta)
	}
	if len(got.Checks) != 1 || got.Checks[0].CheckID != "service:orders-10.0.0.9-8080" {
		t.Fatalf("checks=%+v", got.Checks)
	}
	if !sawPass || !sawToken {
		t.Fatal("expected TTL pass with token")
	}
}

func TestRegisterSelf_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cfg := &Config{
		Register:         true,
		ConsulAddress:    srv.URL,
		AdvertiseAddress: "10.0.0.9",
	}
	_, err := registerSelf(context.Background(), cfg, &config.App{Name: "orders"}, &server.Config{Port: "8080"})
	if err == nil {
		t.Fatal("expected error")
	}
	if isRegisterConfigError(err) {
		t.Fatalf("HTTP error should not be config error: %v", err)
	}
}
