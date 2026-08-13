package docs

import (
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/fitan/fxkit/server"
	"go.uber.org/fx"
	"gopkg.in/yaml.v3"
)

//go:embed openapi.yaml
var openAPIYAML []byte

const scalarHTML = `<!DOCTYPE html>
<html>
<head>
  <title>API Reference</title>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
</head>
<body>
  <script id="api-reference" data-url="/openapi.yaml"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>`

func routes() []server.Route {
	yamlHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(openAPIYAML)
	})

	jsonHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var doc any
		if err := yaml.Unmarshal(openAPIYAML, &doc); err != nil {
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

// Module 在 /docs 提供 Scalar，在 /openapi.{yaml,json} 提供静态 OpenAPI 骨架。
// 生产请嵌入自有 openapi.yaml，或使用 [MergedModule] / [MergedRoutes] 与 Huma live spec 合并。
var Module = fx.Module("fxkit/docs",
	server.ProvideRoutes(routes),
)
