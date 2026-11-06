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
	Enabled bool              `yaml:"enabled"`
	Name    string            `yaml:"name"`
	Path    string            `yaml:"path"` // 默认 "/mcp"
	Headers map[string]string `yaml:"headers"`
	// MaxResponseBytes 单次 tool 调用返回的最大字符数；默认 1 MiB，负数不限制。
	MaxResponseBytes int `yaml:"max_response_bytes"`
}

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

type serverParams struct {
	Config Config
	Mux    *chi.Mux
}

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

// Module 是 MCP 的 Fx 模块。
// 引入后自动从 Huma 提取所有业务路由对象、绑定进程内 chi.Mux 实现 0 网络开销直通反序列化，
// 并可选挂载 SSE HTTP 路由。
var Module = di.Module("mcpx",
	func(i di.Injector) {
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
					if srv != nil && api != nil {
						tools, err := ParseHuma(api)
						if err != nil {
							slog.Error("mcpx: parse huma operations failed", "error", err)
						}
						for _, t := range tools {
							srv.RegisterTool(t)
						}
					}
					c, err := di.Invoke[Config](i)
					if err != nil {
						return fmt.Errorf("mcpx: resolve config: %w", err)
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
