package consulx

import (
	"context"
	"strings"
	"testing"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/server"
)

func TestDetectLocalIP_SkipsEmptyConsul(t *testing.T) {
	ip := detectLocalIP("")
	// 在特殊主机上可能为空；存在时不得是回环地址。
	if ip == "127.0.0.1" || ip == "::1" {
		t.Fatalf("unexpected loopback %q", ip)
	}
}

func TestResolveAdvertise_ConfigWins(t *testing.T) {
	got := resolveAdvertise(&Config{AdvertiseAddress: "9.9.9.9"})
	if got.Addr != "9.9.9.9" || !got.Explicit {
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
	_, err := registerSelf(context.Background(), &Config{Register: true}, &config.App{Name: "orders"}, &server.Config{Port: "8080"})
	if err == nil || !strings.Contains(err.Error(), "consul_address") {
		t.Fatalf("err=%v", err)
	}
	if !isRegisterConfigError(err) {
		t.Fatalf("want config error, got %T %v", err, err)
	}
}
