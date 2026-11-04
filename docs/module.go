package docs

import (
	_ "embed"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
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

// Config 控制 /docs 与 /openapi.{yaml,json} 是否暴露（yaml: docs）。
//
// 默认 **关闭**：文档会把 API 结构（含字段与错误码）暴露给任何能访问服务的人。
// 生产环境应显式开启，并配合网关/鉴权保护。需要自定义 base spec 时用 [MergedModule]
// （它是显式调用，不受本开关影响）。
type Config struct {
	Enabled bool `yaml:"enabled"`
}

// SetDefaults 默认不暴露文档端点。
func (c *Config) SetDefaults() {
	c.Enabled = false
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
