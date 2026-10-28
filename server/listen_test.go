package server

import (
	"testing"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/go-chi/chi/v5"
)

func TestNewHTTPServer_SkipListen(t *testing.T) {
	t.Parallel()
	mux := chi.NewRouter()
	lc := di.NewLifecycle()
	srv := NewHTTPServer(ServerParams{
		Lifecycle: lc,
		Server:    &Config{Port: "18080"},
		Mux:       mux,
		Runtime:   &Runtime{SkipListen: true},
	})
	if srv == nil || srv.Server == nil {
		t.Fatal("nil server")
	}
}

func TestNewHTTPServer_listensByDefault(t *testing.T) {
	t.Parallel()
	lc := di.NewLifecycle()
	srv := NewHTTPServer(ServerParams{
		Lifecycle: lc,
		Server:    &Config{Port: "18081"},
		Mux:       chi.NewRouter(),
	})
	if srv == nil || srv.Server == nil {
		t.Fatal("nil server")
	}
}

func TestNewHTTPServer_AppliesWriteTimeout(t *testing.T) {
	t.Parallel()
	srv := NewHTTPServer(ServerParams{
		Lifecycle: di.NewLifecycle(),
		Server:    &Config{Port: "18082", WriteTimeout: 5 * time.Second},
		Mux:       chi.NewRouter(),
		Runtime:   &Runtime{SkipListen: true},
	})
	if srv.WriteTimeout != 5*time.Second {
		t.Fatalf("WriteTimeout=%v", srv.WriteTimeout)
	}
	fallback := NewHTTPServer(ServerParams{
		Lifecycle: di.NewLifecycle(),
		Server:    &Config{Port: "18083"},
		Mux:       chi.NewRouter(),
		Runtime:   &Runtime{SkipListen: true},
	})
	if fallback.WriteTimeout != defaultWriteTimeout {
		t.Fatalf("default WriteTimeout=%v", fallback.WriteTimeout)
	}
}
