package hatchetx

import (
	"os"
	"testing"

	"github.com/fitan/fxkit/config"
)

func TestApplyHatchetEnv_SetsWhenEmpty(t *testing.T) {
	t.Setenv("HATCHET_CLIENT_TOKEN", "")
	t.Setenv("HATCHET_CLIENT_HOST_PORT", "")
	t.Setenv("HATCHET_CLIENT_NAMESPACE", "")

	err := applyHatchetEnv(config.HatchetConfig{
		Token:     "tok",
		HostPort:  "localhost:7077",
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
	if got := os.Getenv("HATCHET_CLIENT_NAMESPACE"); got != "ns" {
		t.Fatalf("namespace=%q", got)
	}
}

func TestApplyHatchetEnv_DoesNotOverwrite(t *testing.T) {
	t.Setenv("HATCHET_CLIENT_TOKEN", "from-env")
	t.Setenv("HATCHET_CLIENT_HOST_PORT", "env:1")

	err := applyHatchetEnv(config.HatchetConfig{
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
	err := applyHatchetEnv(config.HatchetConfig{HostPort: "not-a-host-port"})
	if err == nil {
		t.Fatal("expected error")
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
