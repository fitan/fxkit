package mcpx

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/server"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
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
	fx.In
	Config Config
	Mux    *chi.Mux `optional:"true"`
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
	fx.In
	Lc     fx.Lifecycle
	Server *Server
	API    huma.API `optional:"true"`
}

func syncHumaTools(p syncParams) {
	if p.Server == nil || p.API == nil {
		return
	}
	// 在 OnStart hook 中同步工具，确保此时所有业务模块的 huma registrars 均已执行完成
	p.Lc.Append(fx.Hook{
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
var Module = fx.Module("mcpx",
	fx.Provide(provideConfig),
	fx.Provide(provideServer),
	fx.Invoke(syncHumaTools),
	server.ProvideRoutes(provideRoutes),
)
