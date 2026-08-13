package consulx

import (
	"context"
	"strings"
	"testing"

	"github.com/fitan/fxkit/config"
)

func TestDetectLocalIP_SkipsEmptyConsul(t *testing.T) {
	ip := detectLocalIP("")
	// May be empty on exotic hosts; when present must not be loopback.
	if ip == "127.0.0.1" || ip == "::1" {
		t.Fatalf("unexpected loopback %q", ip)
	}
}

func TestResolveAdvertise_EnvWins(t *testing.T) {
	t.Setenv("FXKIT_ADVERTISE_ADDRESS", "9.9.9.9")
	if got := resolveAdvertise(nil); got.Addr != "9.9.9.9" || !got.Explicit {
		t.Fatalf("got %+v", got)
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	if !isLoopbackAddr("127.0.0.1") || !isLoopbackAddr("localhost") || !isLoopbackAddr("::1") {
		t.Fatal("expected loopback")
	}
	if isLoopbackAddr("10.0.0.1") {
		t.Fatal("10.0.0.1 is not loopback")
	}
}

func TestRegisterSelf_RequiresConsulAddress(t *testing.T) {
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Viper().Set("discovery.register", true)
	cfg.Viper().Set("discovery.consul_address", "")
	if err := cfg.Sync(); err != nil {
		t.Fatal(err)
	}
	_, err = registerSelf(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "consul_address") {
		t.Fatalf("err=%v", err)
	}
}
