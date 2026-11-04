package consulx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx/internal/fakeconsul"
	"github.com/fitan/fxkit/server"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func registerTestConfig() (*Config, *config.App, *server.Config) {
	return &Config{
			Register:         true,
			ConsulAddress:    "http://consul.test",
			AdvertiseAddress: "10.0.0.9",
		},
		&config.App{Name: "orders"},
		&server.Config{Port: "8080"}
}

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

	fake := fakeconsul.New()
	cfg, app, srvCfg := registerTestConfig()

	id, err := registerSelfWithClient(context.Background(), fake.Client(), cfg, app, srvCfg)
	if err != nil {
		t.Fatal(err)
	}
	if id != "orders-10.0.0.9-8080" {
		t.Fatalf("id=%q", id)
	}

	var got registerServiceRequest
	if raw := fake.Registration(0); raw == nil {
		t.Fatal("no registration request recorded")
	} else if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
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
	if len(got.Tags) < 2 || got.Tags[0] != "fxkit" || got.Tags[1] != "http" {
		t.Fatalf("tags=%v", got.Tags)
	}

	passes := fake.PassCallsSnapshot()
	if len(passes) == 0 || passes[0] != "service:"+id {
		t.Fatalf("expected TTL pass for %q, got %v", "service:"+id, passes)
	}
	tokens := fake.AllTokens()
	if len(tokens) == 0 {
		t.Fatal("expected requests carrying token")
	}
	for _, tok := range tokens {
		if tok != "acl-token" {
			t.Fatalf("missing token on some request: %v", tokens)
		}
	}
}

func TestRegisterSelf_HTTPError(t *testing.T) {
	fake := fakeconsul.New()
	fake.SetRegisterStatus(500)
	cfg, app, srvCfg := registerTestConfig()

	_, err := registerSelfWithClient(context.Background(), fake.Client(), cfg, app, srvCfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if isRegisterConfigError(err) {
		t.Fatalf("HTTP error should not be config error: %v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected status in error, got %v", err)
	}
}

func TestRegisterSelf_MergesExtraTags(t *testing.T) {
	fake := fakeconsul.New()
	cfg, app, srvCfg := registerTestConfig()
	cfg.Tags = []string{"traefik.enable=true", "http", " fxkit "}

	if _, err := registerSelfWithClient(context.Background(), fake.Client(), cfg, app, srvCfg); err != nil {
		t.Fatal(err)
	}

	var got registerServiceRequest
	if err := json.Unmarshal(fake.Registration(0), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"fxkit", "http", "traefik.enable=true"}
	if len(got.Tags) != len(want) {
		t.Fatalf("tags=%v", got.Tags)
	}
	for i, tname := range want {
		if got.Tags[i] != tname {
			t.Fatalf("tags=%v", got.Tags)
		}
	}
}

func TestMergeRegisterTags(t *testing.T) {
	got := mergeRegisterTags(nil)
	if len(got) != 2 || got[0] != "fxkit" || got[1] != "http" {
		t.Fatalf("%v", got)
	}
}

// P1-11：check/service 已被 Consul 移除（404）时，TTL 心跳应重新注册。
func TestTTLPassLoop_ReregistersWhenCheckMissing(t *testing.T) {
	fake := fakeconsul.New()
	fake.NotFoundUntilRegistered = true

	cfg, app, srvCfg := registerTestConfig()
	id := consulServiceID("orders", "10.0.0.9", 8080)

	reregister := func(ctx context.Context) (string, error) {
		return registerSelfWithClient(ctx, fake.Client(), cfg, app, srvCfg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ttlPassLoopWithClient(ctx, fake.Client(), 2*time.Millisecond, cfg, id, reregister)

	if !waitUntil(t, 2*time.Second, func() bool { return fake.RegisterCount() >= 1 }) {
		t.Fatal("service was not re-registered after 404")
	}
	// 注册成功后 check/pass 恢复，不应继续刷注册。
	time.Sleep(30 * time.Millisecond)
	if got := fake.RegisterCount(); got != 1 {
		t.Fatalf("expected exactly one re-registration, got %d", got)
	}
}

// P1-11：连续失败升级为 Warn，并带上失败次数（避免刷屏）。
func TestTTLPassLoop_WarnsAfterConsecutiveFailures(t *testing.T) {
	fake := fakeconsul.New()
	fake.SetPassStatus(500)

	var buf syncBuffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	cfg, _, _ := registerTestConfig()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ttlPassLoopWithClient(ctx, fake.Client(), 2*time.Millisecond, cfg, "svc-id", nil)

	if !waitUntil(t, 2*time.Second, func() bool { return fake.PassCallCount() >= ttlWarnAfterFailures+1 }) {
		t.Fatal("ttl pass was not exercised enough")
	}
	if !waitUntil(t, 2*time.Second, func() bool {
		out := buf.String()
		return strings.Contains(out, "consul ttl pass failed") && strings.Contains(out, "failures=2")
	}) {
		t.Fatalf("expected Warn with failures count, logs:\n%s", buf.String())
	}
}
