package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/gormx"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
)

// HealthParams 在共享 mux 上装配存活与就绪探针。
type HealthParams struct {
	fx.In
	Mux    *chi.Mux
	Client *gormx.Client `optional:"true"`
}

// RegisterHealth 挂载 /healthz（存活）、/readyz（就绪）与 GET /version。
// 可选 gormx client 已配置但 DB ping 失败时，/readyz 返回 503。
func RegisterHealth(p HealthParams) {
	p.Mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	p.Mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if p.Client != nil && p.Client.Pool() != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			sqlDB, err := p.Client.Pool().DB()
			if err != nil {
				http.Error(w, "db unavailable", http.StatusServiceUnavailable)
				return
			}
			if err := sqlDB.PingContext(ctx); err != nil {
				http.Error(w, "db ping failed", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	p.Mux.Method(http.MethodGet, "/version", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		info := buildinfo.Get()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	}))
}
