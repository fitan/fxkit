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

// jsonBytes 返回合并后的 JSON 表示（启动后首次请求时转换并缓存）。
func (m *mergedSpec) jsonBytes() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.json != nil {
		return m.json, nil
	}
	yamlBytes := m.yaml
	if yamlBytes == nil {
		if m.api == nil {
			yamlBytes = m.baseYAML
		} else {
			b, err := openapi.MergeBaseHuma(m.baseYAML, m.api)
			if err != nil {
				return nil, err
			}
			yamlBytes = b
			m.yaml = b
		}
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
		_, _ = w.Write([]byte(scalarHTML))
	})

	return []server.Route{
		{Pattern: "/docs", Handler: docsHandler},
		{Pattern: "/docs/", Handler: docsHandler},
		{Pattern: "/openapi.yaml", Handler: yamlHandler},
		{Pattern: "/openapi.json", Handler: jsonHandler},
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
				api, _ := di.Invoke[huma.API](i)
				server.ProvideRoutes(MergedRoutes(baseYAML, api))(i)
				return nil
			},
		})
		return
	}
	api, _ := di.Invoke[huma.API](i)
	server.ProvideRoutes(MergedRoutes(baseYAML, api))(i)
}
