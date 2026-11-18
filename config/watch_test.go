package config_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
)

type watchApp struct {
	Name string `yaml:"name"`
}

func TestProvideWatchDynamic(t *testing.T) {
	root := di.New(config.Module, config.ProvideWatch[watchApp]("app"))
	d, err := di.Invoke[config.Dynamic[watchApp]](root)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil {
		t.Fatal("nil Dynamic")
	}
	if d.Get() == nil {
		t.Fatal("nil snapshot")
	}
}

func TestWatchConsulUpdates(t *testing.T) {
	update := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("index") {
		case "", "0":
			w.Header().Set("X-Consul-Index", "1")
			_, _ = w.Write([]byte("app:\n  name: v1\n"))
		case "1":
			select {
			case <-update:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("X-Consul-Index", "2")
			_, _ = w.Write([]byte("app:\n  name: v2\n"))
		default:
			<-r.Context().Done()
		}
	}))
	defer srv.Close()

	cfg, err := config.New(config.Options{ConsulAddress: srv.URL, ConsulConfigKey: "app"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := cfg.Watch[watchApp]("app")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if got := w.Get(); got == nil || got.Name != "v1" {
		t.Fatalf("initial = %+v, want v1", got)
	}
	close(update)

	deadline := time.Now().Add(3 * time.Second)
	for {
		if got := w.Get(); got != nil && got.Name == "v2" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("watch did not update: %+v", w.Get())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
