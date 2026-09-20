package mcpx

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/server"
	"go.uber.org/fx"
)

// Config 定义在 application config 中的 mcp 配置段。
type Config struct {
	Enabled bool              `koanf:"enabled"`
	Name    string            `koanf:"name"`
	Path    string            `koanf:"path"` // 默认 "/mcp"
	BaseURL string            `koanf:"base_url"`
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
	API    huma.API `optional:"true"`
}

func provideServer(p serverParams) (*Server, error) {
	var tools []Tool
	if p.API != nil {
		extracted, err := ParseHuma(p.API)
		if err == nil {
			tools = extracted
		}
	}

	baseURL := p.Config.BaseURL
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}

	name := p.Config.Name
	if name == "" {
		name = "fxkit-service"
	}

	srv := NewServer(ServerConfig{
		Name:    name,
		BaseURL: baseURL,
		Headers: p.Config.Headers,
	}, tools)

	return srv, nil
}

func provideRoutes(srv *Server, cfg Config) []server.Route {
	if !cfg.Enabled && cfg.Path == "" {
		return nil
	}
	path := cfg.Path
	if path == "" {
		path = "/mcp"
	}
	return []server.Route{
		{
			Pattern: path,
			Handler: srv.HTTPHandler(),
			Methods: []string{"POST"},
		},
	}
}

// Module 是 MCP 的 Fx 模块。引入后自动解析 Huma 路由，并可选在 /mcp 暴露 HTTP JSON-RPC 2.0 端点。
var Module = fx.Module("mcpx",
	fx.Provide(provideConfig),
	fx.Provide(provideServer),
	server.ProvideRoutes(provideRoutes),
)
