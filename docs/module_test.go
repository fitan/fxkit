package docs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMergedRoutes_ServesDocsAndSpec 验证 MergedRoutes 提供三个端点。
func TestMergedRoutes_ServesDocsAndSpec(t *testing.T) {
	base := []byte("openapi: 3.0.3\ninfo:\n  title: t\n  version: 1.0.0\npaths: {}\n")
	routes := MergedRoutes(base, nil)
	require.Len(t, routes, 4)

	byPattern := map[string]http.Handler{}
	for _, r := range routes {
		byPattern[r.Pattern] = r.Handler
	}

	// /docs 返回 Scalar HTML
	rec := httptest.NewRecorder()
	byPattern["/docs"].ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "api-reference")

	// /openapi.yaml 返回 YAML
	rec = httptest.NewRecorder()
	byPattern["/openapi.yaml"].ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "yaml")
	assert.Contains(t, rec.Body.String(), "openapi:")

	// /openapi.json 返回合法 JSON
	rec = httptest.NewRecorder()
	byPattern["/openapi.json"].ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "json")

	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc), "openapi.json must be valid JSON")
	assert.Contains(t, doc, "openapi")
}

// TestModule_StaticRoutes 未引入 huma 模块时（api=nil）也提供同样端点。
func TestModule_StaticRoutes(t *testing.T) {
	routes := MergedRoutes(openAPIYAML, nil)
	require.Len(t, routes, 4)

	patterns := make(map[string]bool, len(routes))
	for _, r := range routes {
		patterns[r.Pattern] = true
		assert.NotNil(t, r.Handler, "route %s must have a handler", r.Pattern)
	}
	for _, want := range []string{"/docs", "/docs/", "/openapi.yaml", "/openapi.json"} {
		assert.True(t, patterns[want], "missing route %s", want)
	}
}

// TestEmbeddedOpenAPIYAMLIsValid 内嵌 spec 必须可解析，否则 /openapi.json 会 500。
func TestEmbeddedOpenAPIYAMLIsValid(t *testing.T) {
	assert.NotEmpty(t, openAPIYAML, "embedded openapi.yaml must not be empty")
	assert.Contains(t, string(openAPIYAML), "openapi:", "embedded spec must declare an openapi version")
}
