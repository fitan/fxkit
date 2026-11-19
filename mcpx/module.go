// Package mcpx 把 Huma/OpenAPI 路由导出为大模型工具，并提供 MCP Server。
//
// [ParseHuma] / [ParseOpenAPI] 从 operation 提取 [Tool]；[ToOpenAI]、[ToClaude]、
// [ToGemini] 转成各厂商的 Function Calling 格式；[InProcessDispatcher] 用内存 HTTP
// 请求直通本服务的 chi.Mux，复用 Huma 的路径/查询/请求体反序列化。
// [Module] 引入后自动同步工具，并在 mcp.enabled 为 true 时把 SSE 端点挂到 mcp.path；
// stdio 由 `fxkit-app mcp` 调用 [Server.ServeStdio] 启动。
package mcpx

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
	"github.com/go-chi/chi/v5"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Config 定义在 application config 中的 mcp 配置段。
type Config struct {
	// Enabled 为 true 时才把 SSE 端点挂到 [Config.Path]；false 不注册 HTTP 路由，仅保留 stdio。
	Enabled bool `yaml:"enabled"`
	// Name 是 MCP server 名称；空则回退 "fxkit-service"。
	Name string `yaml:"name"`
	Path string `yaml:"path"` // 默认 "/mcp"
	// Headers 是转发进程内 HTTP 请求时附带的固定请求头（如 Authorization）；
	// 调用者身份头最后写入，会覆盖同名项。
	Headers map[string]string `yaml:"headers"`
	// MaxResponseBytes 单次 tool 调用返回的最大字节数；默认 1 MiB，负数不限制。
	MaxResponseBytes int `yaml:"max_response_bytes"`
	// IncludeTools 非空时只暴露列出的 operationId（白名单）。
	// 未配置时暴露全部 Huma operation（包括 authz 管理接口）——
	// 生产环境建议显式配置白名单，或由网关对 /mcp 做鉴权。
	IncludeTools []string `yaml:"include_tools"`
	// ExcludeTools 排除列出的 operationId；优先级高于 IncludeTools。
	ExcludeTools []string `yaml:"exclude_tools"`
}

// provideConfig 从 config 的 mcp 段解码配置，并补齐 path 前缀与默认值。
func provideConfig(cfg *config.Config) (Config, error) {
	c := Config{Path: "/mcp"}
	if cfg != nil {
		if err := cfg.UnmarshalKey("mcp", &c); err != nil {
			return Config{}, fmt.Errorf("mcpx: decode mcp config: %w", err)
		}
	}
	c.Path = strings.TrimSpace(c.Path)
	if c.Path == "" {
		c.Path = "/mcp"
	}
	if !strings.HasPrefix(c.Path, "/") {
		c.Path = "/" + c.Path
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = 1 << 20
	}
	return c, nil
}

// serverParams 是 [provideServer] 的输入。
type serverParams struct {
	// Config 是已解码并补齐默认值的 mcp 配置。
	Config Config
	// Mux 是进程内直通的 chi mux；nil 时不配置分发器。
	Mux *chi.Mux
}

// provideServer 按配置与 chi.Mux 构造 Server，名称缺省为 "fxkit-service"。
func provideServer(p serverParams) (*Server, error) {
	name := p.Config.Name
	if name == "" {
		name = "fxkit-service"
	}

	var handler http.Handler
	if p.Mux != nil {
		handler = p.Mux
	}

	srv := NewServer(ServerConfig{
		Name:             name,
		Handler:          handler,
		Headers:          p.Config.Headers,
		MaxResponseBytes: p.Config.MaxResponseBytes,
	}, nil)

	return srv, nil
}

// provideRoutes 在 mcp.enabled 时返回挂载 SSE 端点的路由，否则返回 nil。
func provideRoutes(srv *Server, cfg Config) []server.Route {
	// mcp.enabled 未开启时不对外暴露 HTTP 端点（stdio 仍可用 `fxkit-app mcp`）。
	if !cfg.Enabled {
		return nil
	}
	path := cfg.Path
	if path == "" {
		path = "/mcp"
	}
	// 必须把 base path 告诉 mcp-go：SSE server 按精确路径（basePath+/sse、
	// basePath+/message）匹配，仅注册 "/mcp/*" 而不设 basePath 会导致 404。
	sse := srv.SSEServer(mcpserver.WithStaticBasePath(path))
	return []server.Route{
		server.PrefixRoute(path, sse),
	}
}

// toolEnabled 按配置的白/黑名单过滤工具（按 operationId 精确匹配）。
func toolEnabled(cfg Config, name string) bool {
	for _, ex := range cfg.ExcludeTools {
		if strings.TrimSpace(ex) == name {
			return false
		}
	}
	if len(cfg.IncludeTools) == 0 {
		return true
	}
	for _, in := range cfg.IncludeTools {
		if strings.TrimSpace(in) == name {
			return true
		}
	}
	return false
}

// Module 是 MCP 的 Fx 模块。
//
// 引入后自动从 Huma 提取所有业务路由对象、绑定进程内 chi.Mux 实现 0 网络开销直通反序列化，
// 并可选挂载 SSE HTTP 路由。
var Module = di.Module("mcpx",
	func(i di.Injector) {
		// 登记配置消费方，使 --strict 不会把 mcp 段误报为死配置。
		config.RegisterConsumer[Config]("mcp")
		di.Provide(i, func(i di.Injector) (Config, error) {
			cfg, err := di.Invoke[*config.Config](i)
			if err != nil {
				return Config{}, err
			}
			return provideConfig(cfg)
		})
		di.Provide(i, func(i di.Injector) (*Server, error) {
			c, _ := di.Invoke[Config](i)
			mux, _ := di.Invoke[*chi.Mux](i)
			return provideServer(serverParams{Config: c, Mux: mux})
		})

		// 工具同步与 SSE 路由挂载都依赖「装配完成后的完整 group」，
		// 因此统一放到 Lifecycle.PreStart。
		lc := di.MustInvoke[*di.Lifecycle](i)
		if lc != nil {
			lc.AppendPreStartPhase(di.PhasePostWire, di.Hook{
				OnStart: func(context.Context) error {
					srv, _ := di.Invoke[*Server](i)
					api, _ := di.Invoke[huma.API](i)
					c, err := di.Invoke[Config](i)
					if err != nil {
						return fmt.Errorf("mcpx: resolve config: %w", err)
					}
					if srv != nil && api != nil {
						tools, err := ParseHuma(api)
						if err != nil {
							slog.Error("mcpx: parse huma operations failed", "error", err)
						}
						for _, t := range tools {
							if !toolEnabled(c, t.Name) {
								continue
							}
							srv.RegisterTool(t)
						}
					}
					if srv != nil {
						server.ProvideRoutes(provideRoutes(srv, c)...)(i)
					}
					return nil
				},
			})
		}
	},
)
