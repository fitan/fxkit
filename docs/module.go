// Package docs 提供 /docs（Scalar UI）与 /openapi.{yaml,json} 端点，
// 把内嵌的 base OpenAPI 与当前生效的 Huma spec 合并后输出。
//
// [Module] 受配置 docs.enabled 控制，默认关闭；[MergedModule] 是显式调用，
// 不受该开关影响，适合需要自定义 base spec 的场景。
package docs

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
)

// openAPIYAML 是内嵌的 base OpenAPI 规范（openapi.yaml）。
//
//go:embed openapi.yaml
var openAPIYAML []byte

// defaultScalarURL 是 Scalar UI 脚本的默认 CDN 地址。
const defaultScalarURL = "https://cdn.jsdelivr.net/npm/@scalar/api-reference"

// scalarHTML 生成 Scalar UI 页面。base path 与脚本地址均可配置，
// 以适配网关前缀、内网自托管与 SRI 校验。
func scalarHTML(opts RouteOptions) string {
	base := normalizeBasePath(opts.BasePath)
	specURL := joinURLPath(base, "/openapi.yaml")
	script := strings.TrimSpace(opts.ScalarURL)
	if script == "" {
		script = defaultScalarURL
	}
	integrity := ""
	if sri := strings.TrimSpace(opts.ScalarSRI); sri != "" {
		integrity = fmt.Sprintf(` integrity=%q crossorigin="anonymous"`, sri)
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <title>API Reference</title>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
</head>
<body>
  <script id="api-reference" data-url=%q></script>
  <script src=%q%s></script>
</body>
</html>`, specURL, script, integrity)
}

// normalizeBasePath 规范化挂载前缀：补前导斜杠、去尾部斜杠；空串与 "/" 返回空串。
func normalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(p, "/")
}

// joinURLPath 把前缀与路径拼接；base 为空时原样返回 p。
func joinURLPath(base, p string) string {
	if base == "" {
		return p
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return base + p
}

// Config 控制 /docs 与 /openapi.{yaml,json} 是否暴露（yaml: docs）。
//
// 默认 **关闭**：文档会把 API 结构（含字段与错误码）暴露给任何能访问服务的人。
// 生产环境应显式开启，并配合网关/鉴权保护。需要自定义 base spec 时用 [MergedModule]
// （它是显式调用，不受本开关影响）。
type Config struct {
	// Enabled 为 true 时挂载 /docs 与 /openapi.{yaml,json}；默认 false。
	Enabled bool `yaml:"enabled"`
	// BasePath 挂载前缀（例如 "/api"）。默认空，表示直接挂在根路径。
	BasePath string `yaml:"base_path"`
	// ScalarURL Scalar UI 脚本地址；默认 jsdelivr CDN。内网可指向自托管资源。
	ScalarURL string `yaml:"scalar_url"`
	// ScalarSRI 可选的 Subresource Integrity hash（如 "sha384-..."）。
	ScalarSRI string `yaml:"scalar_sri"`
}

// SetDefaults 默认不暴露文档端点。
func (c *Config) SetDefaults() {
	c.Enabled = false
	c.ScalarURL = defaultScalarURL
}

// Module 在 docs.enabled=true 时提供 /docs 与 /openapi.{yaml,json}
// （引入 huma.Module 时自动合并 live spec）。
var Module = di.Module("fxkit/docs",
	config.Provide[Config]("docs"),
	func(i di.Injector) {
		cfg, err := di.Invoke[*Config](i)
		if err != nil || cfg == nil || !cfg.Enabled {
			return
		}
		registerMerged(i, openAPIYAML)
	},
)
