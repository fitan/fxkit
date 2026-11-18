package docs

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/openapi"
	"github.com/fitan/fxkit/server"
	"gopkg.in/yaml.v3"
)

type mergedSpec struct {
	mu       sync.Mutex
	baseYAML []byte
	api      huma.API
	yaml     []byte
	json     []byte
}

// bytes 返回合并后的 YAML；成功结果缓存，失败不缓存（避免一次抖动永久 500）。
func (m *mergedSpec) bytes() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mergedLocked()
}

// mergedLocked 返回合并后的 YAML，调用方必须持有 m.mu。
//
// 抽出来是为了让 YAML 与 JSON 两个入口共用同一份合并与缓存逻辑
// （早期 jsonBytes 把三个分支又写了一遍）。
func (m *mergedSpec) mergedLocked() ([]byte, error) {
	if m.yaml != nil {
		return m.yaml, nil
	}
	if m.api == nil {
		// 没有 Huma 模块：退化为静态骨架。
		m.yaml = m.baseYAML
		return m.yaml, nil
	}
	b, err := openapi.MergeBaseHuma(m.baseYAML, m.api)
	if err != nil {
		return nil, err
	}
	m.yaml = b
	return b, nil
}

// jsonBytes 返回合并后的 JSON 表示（首次请求时转换并缓存）。
func (m *mergedSpec) jsonBytes() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.json != nil {
		return m.json, nil
	}
	yamlBytes, err := m.mergedLocked()
	if err != nil {
		return nil, err
	}
	var doc any
	if err := yaml.Unmarshal(yamlBytes, &doc); err != nil {
		return nil, err
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	m.json = b
	return b, nil
}

// RouteOptions 控制文档挂载路径与 Scalar 资源。
type RouteOptions struct {
	BasePath  string
	ScalarURL string
	ScalarSRI string
}

// MergedRoutes 在 /docs 提供 Scalar，在 /openapi.{yaml,json} 提供 OpenAPI，
// 把可选的 baseYAML 与当前生效的 Huma spec 合并。
func MergedRoutes(baseYAML []byte, api huma.API) []server.Route {
	return MergedRoutesWithOptions(baseYAML, api, RouteOptions{})
}

// MergedRoutesWithOptions 是 [MergedRoutes] 的可配置版本。
func MergedRoutesWithOptions(baseYAML []byte, api huma.API, opts RouteOptions) []server.Route {
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
		b, err := spec.jsonBytes()
		if err != nil {
			slog.Error("docs: merge openapi failed", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(b)
	})

	docsHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(scalarHTML(opts)))
	})

	base := normalizeBasePath(opts.BasePath)
	docsPath := joinURLPath(base, "/docs")
	yamlPath := joinURLPath(base, "/openapi.yaml")
	jsonPath := joinURLPath(base, "/openapi.json")

	return []server.Route{
		{Pattern: docsPath, Handler: docsHandler},
		{Pattern: docsPath + "/", Handler: docsHandler},
		{Pattern: yamlPath, Handler: yamlHandler},
		{Pattern: jsonPath, Handler: jsonHandler},
	}
}

// MergedModule 显式提供 /docs 与 /openapi.{yaml,json}，并把 baseYAML 与 live Huma spec 合并。
//
// 它是**显式**调用，不受 `docs.enabled` 开关影响（适合“我就是要文档”的场景，
// 例如 example 的自定义 bundle）。若只想在配置里打开默认形态，用 [Module]。
func MergedModule(baseYAML []byte) di.Option {
	return di.Module("fxkit/docs", func(i di.Injector) {
		registerMerged(i, baseYAML)
	})
}

// registerMerged 把合并后的路由贡献到 server.Route group。
//
// 路由必须由完整的 huma spec 生成，而 huma 在 PreStart 才安装 registrar；
// 因此推迟到 PhasePostWire 再贡献。
func registerMerged(i di.Injector, baseYAML []byte) {
	if lc, err := di.Invoke[*di.Lifecycle](i); err == nil && lc != nil {
		lc.AppendPreStartPhase(di.PhasePostWire, di.Hook{
			OnStart: func(context.Context) error {
				server.ProvideRoutes(mergedRoutesFor(i, baseYAML)...)(i)
				return nil
			},
		})
		return
	}
	server.ProvideRoutes(mergedRoutesFor(i, baseYAML)...)(i)
}

func mergedRoutesFor(i di.Injector, baseYAML []byte) []server.Route {
	api, _ := di.Invoke[huma.API](i)
	cfg, _ := di.Invoke[*Config](i)
	opts := RouteOptions{}
	if cfg != nil {
		opts = RouteOptions{
			BasePath:  cfg.BasePath,
			ScalarURL: cfg.ScalarURL,
			ScalarSRI: cfg.ScalarSRI,
		}
	}
	return MergedRoutesWithOptions(baseYAML, api, opts)
}
