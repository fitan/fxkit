package hatchetx

import (
	"os"
	"testing"
)

func TestApplyHatchetEnv_SetsWhenEmpty(t *testing.T) {
	t.Setenv("HATCHET_CLIENT_TOKEN", "")
	t.Setenv("HATCHET_CLIENT_HOST_PORT", "")
	t.Setenv("HATCHET_CLIENT_NAMESPACE", "")
	t.Setenv("HATCHET_CLIENT_SERVER_URL", "")

	err := applyHatchetEnv(Config{
		Token:     "tok",
		HostPort:  "localhost:7077",
		ServerURL: "http://127.0.0.1:8088",
		Namespace: "ns",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HATCHET_CLIENT_TOKEN"); got != "tok" {
		t.Fatalf("token=%q", got)
	}
	if got := os.Getenv("HATCHET_CLIENT_HOST_PORT"); got != "localhost:7077" {
		t.Fatalf("host_port=%q", got)
	}
	if got := os.Getenv("HATCHET_CLIENT_SERVER_URL"); got != "http://127.0.0.1:8088" {
		t.Fatalf("server_url=%q", got)
	}
	if got := os.Getenv("HATCHET_CLIENT_NAMESPACE"); got != "ns" {
		t.Fatalf("namespace=%q", got)
	}
}

func TestApplyHatchetEnv_DoesNotOverwrite(t *testing.T) {
	t.Setenv("HATCHET_CLIENT_TOKEN", "from-env")
	t.Setenv("HATCHET_CLIENT_HOST_PORT", "env:1")

	err := applyHatchetEnv(Config{
		Token:    "from-yaml",
		HostPort: "yaml:7077",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HATCHET_CLIENT_TOKEN"); got != "from-env" {
		t.Fatalf("token overwritten: %q", got)
	}
	if got := os.Getenv("HATCHET_CLIENT_HOST_PORT"); got != "env:1" {
		t.Fatalf("host_port overwritten: %q", got)
	}
}

func TestApplyHatchetEnv_InvalidHostPort(t *testing.T) {
	err := applyHatchetEnv(Config{HostPort: "not-a-host-port"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConfigValidate_EnabledRequiresToken(t *testing.T) {
	t.Setenv("HATCHET_CLIENT_TOKEN", "")
	c := Config{Enabled: true}
	c.SetDefaults()
	if err := c.Validate(); err == nil {
		t.Fatal("expected token error")
	}
	c.Token = "tok"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyHatchetEnv_RemoteHostNoDefaultTLS(t *testing.T) {
	t.Setenv("HATCHET_CLIENT_TLS_STRATEGY", "")
	t.Setenv("HATCHET_CLIENT_HOST_PORT", "")
	err := applyHatchetEnv(Config{HostPort: "10.170.34.223:7077"})
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HATCHET_CLIENT_TLS_STRATEGY"); got != "" {
		t.Fatalf("remote host should not default TLS none, got %q", got)
	}
}

func TestApplyHatchetEnv_TLSStrategyYaml(t *testing.T) {
	t.Setenv("HATCHET_CLIENT_TLS_STRATEGY", "")
	err := applyHatchetEnv(Config{HostPort: "10.170.34.223:7077", TLSStrategy: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HATCHET_CLIENT_TLS_STRATEGY"); got != "none" {
		t.Fatalf("tls_strategy=%q", got)
	}
}

func TestConfigValidate_TLSStrategy(t *testing.T) {
	c := Config{TLSStrategy: "bogus"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected tls_strategy error")
	}
	c.TLSStrategy = "none"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNewClient_Disabled(t *testing.T) {
	c, err := NewClient(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled() {
		t.Fatal("expected disabled client")
	}
}
