// Package hello 是最小脚手架服务：基于 chi 的单个 JSON HTTP greet 端点。
// 复制/重命名后将 fx.Module 接入 cmd/main.go。
package hello

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/server"
	"github.com/go-chi/chi/v5"
)

type Service struct {
	cfg *config.Config
}

func NewService(cfg *config.Config) *Service {
	return &Service{cfg: cfg}
}

type GreetInput struct {
	Name string `path:"name" doc:"Greeting target"`
}

type GreetOutput struct {
	Body struct {
		Greeting string `json:"greeting"`
	}
}

func (s *Service) Greet(ctx context.Context, in *GreetInput) (*GreetOutput, error) {
	name := in.Name
	if name == "" {
		name = "World"
	}
	out := &GreetOutput{}
	out.Body.Greeting = fmt.Sprintf("Hello, %s!", name)
	return out, nil
}

func helloRoutes(svc *Service) []server.Route {
	return []server.Route{{
		Pattern: "/greet/{name}",
		Methods: []string{http.MethodGet},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := chi.URLParam(r, "name")
			out, err := svc.Greet(r.Context(), &GreetInput{Name: name})
			if err != nil {
				fxerrors.WriteError(w, r, err)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(out)
		}),
	}}
}

var Module = fxkit.Service("hello", NewService,
	fxkit.Routes(helloRoutes),
)
