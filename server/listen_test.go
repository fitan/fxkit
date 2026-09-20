package server

import (
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
)

type hookCounter struct{ n int }

func (h *hookCounter) Append(fx.Hook) { h.n++ }

func TestNewHTTPServer_SkipListen(t *testing.T) {
	t.Parallel()
	mux := chi.NewRouter()
	lc := &hookCounter{}
	srv := NewHTTPServer(ServerParams{
		Lifecycle: lc,
		Server:    &Config{Port: "18080"},
		Mux:       mux,
		Runtime:   &Runtime{SkipListen: true},
	})
	if srv == nil || srv.Server == nil {
		t.Fatal("nil server")
	}
	if lc.n != 0 {
		t.Fatalf("SkipListen should not register lifecycle hooks, got %d", lc.n)
	}
}

func TestNewHTTPServer_listensByDefault(t *testing.T) {
	t.Parallel()
	lc := &hookCounter{}
	_ = NewHTTPServer(ServerParams{
		Lifecycle: lc,
		Server:    &Config{Port: "18081"},
		Mux:       chi.NewRouter(),
	})
	if lc.n != 1 {
		t.Fatalf("want 1 listen hook, got %d", lc.n)
	}
}

func TestNewHTTPServer_AppliesWriteTimeout(t *testing.T) {
	t.Parallel()
	srv := NewHTTPServer(ServerParams{
		Lifecycle: &hookCounter{},
		Server:    &Config{Port: "18082", WriteTimeout: 5 * time.Second},
		Mux:       chi.NewRouter(),
		Runtime:   &Runtime{SkipListen: true},
	})
	if srv.WriteTimeout != 5*time.Second {
		t.Fatalf("WriteTimeout=%v", srv.WriteTimeout)
	}
	fallback := NewHTTPServer(ServerParams{
		Lifecycle: &hookCounter{},
		Server:    &Config{Port: "18083"},
		Mux:       chi.NewRouter(),
		Runtime:   &Runtime{SkipListen: true},
	})
	if fallback.WriteTimeout != defaultWriteTimeout {
		t.Fatalf("default WriteTimeout=%v", fallback.WriteTimeout)
	}
}
