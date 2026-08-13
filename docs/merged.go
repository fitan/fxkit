package docs

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/openapi"
	"github.com/fitan/fxkit/server"
	"go.uber.org/fx"
	"gopkg.in/yaml.v3"
)

// MergedParams wires live [huma.API] into merged OpenAPI routes.
type MergedParams struct {
	fx.In
	API huma.API
}

type mergedSpec struct {
	baseYAML []byte
	api      huma.API
	once     sync.Once
	yaml     []byte
	err      error
}

func (m *mergedSpec) bytes() ([]byte, error) {
	m.once.Do(func() {
		m.yaml, m.err = openapi.MergeBaseHuma(m.baseYAML, m.api)
	})
	return m.yaml, m.err
}

// MergedRoutes serves Scalar at /docs and OpenAPI at /openapi.{yaml,json},
// merging optional baseYAML with the live Huma spec.
func MergedRoutes(baseYAML []byte, api huma.API) []server.Route {
	spec := &mergedSpec{baseYAML: baseYAML, api: api}

	yamlHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		b, err := spec.bytes()
		if err != nil {
			slog.Error("docs: merge openapi failed", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(b)
	})

	jsonHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		b, err := spec.bytes()
		if err != nil {
			slog.Error("docs: merge openapi failed", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var doc any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			slog.Error("docs: openapi yaml parse failed", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(doc)
	})

	docsHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(scalarHTML))
	})

	return []server.Route{
		{Pattern: "/docs", Handler: docsHandler},
		{Pattern: "/docs/", Handler: docsHandler},
		{Pattern: "/openapi.yaml", Handler: yamlHandler},
		{Pattern: "/openapi.json", Handler: jsonHandler},
	}
}

// MergedModule is like [Module], but merges baseYAML with [huma.API] at serve time.
// Requires [github.com/fitan/fxkit/huma].Module in the same Fx app when baseYAML
// should be overlaid with live Huma routes. Pass nil/empty baseYAML for Huma-only.
func MergedModule(baseYAML []byte) fx.Option {
	yaml := baseYAML
	return fx.Module("fxkit/docs",
		server.ProvideRoutes(func(p MergedParams) []server.Route {
			return MergedRoutes(yaml, p.API)
		}),
	)
}
