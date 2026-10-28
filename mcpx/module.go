package mcpx

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/server"
	"github.com/go-chi/chi/v5"
	"github.com/fitan/fxkit/di"
)

// Config 定义在 application config 中的 mcp 配置段。
type Config struct {
	Enabled bool              `koanf:"enabled"`
	Name    string            `koanf:"name"`
	Path    string            `koanf:"path"` // 默认 "/mcp"
	Headers map[string]string `koanf:"headers"`
}

func provideConfig(cfg *config.Config) Config {
	var c Config
	c.Path = "/mcp"
	if cfg != nil {
		_ = cfg.UnmarshalKey("mcp", &c)
	}
	return c
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
		Name:    name,
		Handler: handler,
		Headers: p.Config.Headers,
	}, nil)

	return srv, nil
}

type syncParams struct {
	Lc     *di.Lifecycle
	Server *Server
	API    huma.API 
}

func syncHumaTools(p syncParams) {
	if p.Server == nil || p.API == nil {
		return
	}
	// 在 OnStart hook 中同步工具，确保此时所有业务模块的 huma registrars 均已执行完成
	if p.Lc != nil {
		p.Lc.Append(di.Hook{
		OnStart: func(ctx context.Context) error {
			tools, err := ParseHuma(p.API)
			if err == nil {
				for _, t := range tools {
					p.Server.RegisterTool(t)
				}
			}
			return nil
		},
	})
	}
}

func provideRoutes(srv *Server, cfg Config) []server.Route {
	if !cfg.Enabled && cfg.Path == "" {
		return nil
	}
	path := cfg.Path
	if path == "" {
		path = "/mcp"
	}
	sse := srv.SSEServer()
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
			cfg, _ := di.Invoke[*config.Config](i)
			return provideConfig(cfg), nil
		})
		di.Provide(i, func(i di.Injector) (*Server, error) {
			c, _ := di.Invoke[Config](i)
			mux, _ := di.Invoke[*chi.Mux](i)
			return provideServer(serverParams{Config: c, Mux: mux})
		})

		lc, _ := di.Invoke[*di.Lifecycle](i)
		srv, _ := di.Invoke[*Server](i)
		api, _ := di.Invoke[huma.API](i)
		syncHumaTools(syncParams{Lc: lc, Server: srv, API: api})

		c, _ := di.Invoke[Config](i)
		if srv != nil {
			server.ProvideRoutes(provideRoutes(srv, c))(i)
		}
	},
)
