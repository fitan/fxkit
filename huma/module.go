// Package huma 在共享 chi mux 上装配 [danielgtaylor/huma/v2]，handler 感知 fxerrors。
package huma

import (
	"context"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/go-chi/chi/v5"
)

// Registrar 向共享 API 实例挂载一个或多个 Huma operation。
type Registrar interface {
	Register(api huma.API)
}

// FuncRegistrar 将普通 func 适配为通过闭包注册的模块。
type FuncRegistrar func(api huma.API)

func (f FuncRegistrar) Register(api huma.API) { f(api) }

// Middleware 是 Huma 中间件签名（见 [huma.API.UseMiddleware]）。
type Middleware = func(ctx huma.Context, next func(huma.Context))

// MiddlewareRegistrar 在拿到共享 [huma.API] 后构造一个 [Middleware]。
type MiddlewareRegistrar interface {
	Middleware(api huma.API) Middleware
}

// MiddlewareFunc 将普通 func 适配为 [MiddlewareRegistrar]。
type MiddlewareFunc func(api huma.API) Middleware

func (f MiddlewareFunc) Middleware(api huma.API) Middleware { return f(api) }

type registrarParams struct {
	API         huma.API
	Middlewares []MiddlewareRegistrar
	Registrars  []Registrar
}

func invokeRegistrars(p registrarParams) {
	if p.API == nil {
		return
	}
	installed := 0
	for _, mw := range p.Middlewares {
		if mw == nil {
			continue
		}
		if m := mw.Middleware(p.API); m != nil {
			p.API.UseMiddleware(m)
			installed++
		}
	}
	for _, reg := range p.Registrars {
		if reg == nil {
			continue
		}
		reg.Register(p.API)
	}
	slog.Info("huma: registrars complete", "count", len(p.Registrars), "middlewares", installed)
}

// Config 控制 Huma 自身的 spec 端点与响应注入（yaml: huma）。
type Config struct {
	// PublicSpec 暴露 /huma/openapi.{json,yaml}（含 -3.0 变体）。
	// 默认 false：spec 会暴露全部路由与字段结构，应与 docs 一样由网关/鉴权保护。
	//
	// 注意：mcpx 与 docs.MergedModule 读取的是内存中的 api.OpenAPI()，
	// 不依赖这个 HTTP 端点。
	PublicSpec bool `yaml:"public_spec"`

	// SchemaLinks 在响应 JSON 中注入 $schema 字段，并添加 Link 头
	// （Huma 的 SchemaLinkTransformer 默认行为）。
	// 默认 false：多数 API 不需要该字段，且会污染 DTO。
	SchemaLinks bool `yaml:"schema_links"`
}

// SetDefaults 默认不暴露 Huma spec 路由，也不注入 $schema / Link。
func (c *Config) SetDefaults() {
	c.PublicSpec = false
	c.SchemaLinks = false
}

// NewAPI 在共享 chi mux 上构建 Huma API，使用 app 元数据作为 OpenAPI 标题。
//
// 默认关闭 DocsPath、OpenAPIPath，并移除 Huma 的 $schema / Link 注入；
// 需要 Huma 的 schema link 行为时显式设 SchemaLinks=true。
func NewAPI(mux *chi.Mux, app *config.App, cfgs ...*Config) huma.API {
	appName := "app"
	if app != nil && app.Name != "" {
		appName = app.Name
	}
	conf := huma.DefaultConfig(appName, "1.0.0")
	conf.DocsPath = ""
	conf.OpenAPIPath = ""

	var cfg *Config
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}
	if cfg != nil && cfg.PublicSpec {
		conf.OpenAPIPath = "/huma/openapi"
	}
	if cfg == nil || !cfg.SchemaLinks {
		// Huma 的 DefaultConfig 通过 CreateHooks 安装 SchemaLinkTransformer；
		// 清空它即可去掉响应里的 $schema 字段与 Link 头。
		conf.CreateHooks = nil
		conf.Transformers = nil
	}
	return humachi.New(mux, conf)
}

// ProvideRegistrar 发布一个已构造好的 [Registrar]。
// 需要依赖时用 [ProvideRegistrarLazy]（构造函数显式接收 di.Injector）。
func ProvideRegistrar(reg Registrar) di.Option {
	return func(i di.Injector) {
		if reg == nil {
			return
		}
		di.ProvideGroupItem(i, reg)
	}
}

// ProvideRegistrarLazy 在 PreStart（PhaseResolveContributors）求值后可访问完整依赖图。
func ProvideRegistrarLazy(fn func(di.Injector) (Registrar, error)) di.Option {
	return func(i di.Injector) { di.ProvideLazyE[Registrar](i, fn) }
}

// ProvideMiddleware 发布一个已构造好的 [MiddlewareRegistrar]。
// 需要依赖时用 [ProvideMiddlewareLazy]。
func ProvideMiddleware(mw MiddlewareRegistrar) di.Option {
	return func(i di.Injector) {
		if mw == nil {
			return
		}
		di.ProvideGroupItem(i, mw)
	}
}

// ProvideMiddlewareLazy 在 PreStart 求值（可访问完整依赖图）。
func ProvideMiddlewareLazy(fn func(di.Injector) (MiddlewareRegistrar, error)) di.Option {
	return func(i di.Injector) { di.ProvideLazyE[MiddlewareRegistrar](i, fn) }
}

// Module 提供共享 [huma.API]，挂载所有 [MiddlewareRegistrar]，并执行所有 [Registrar] hook。
//
// huma 在 RegisterOperation 时 **快照** api.Middlewares()，因此中间件与 registrar
// 都必须在「所有模块装配完成之后」一次性安装，否则后装配模块的 registrar 会被漏掉、
// 后注册的中间件不会作用到已注册的 operation。
// 这里把安装动作放到 Lifecycle.PreStart（PhaseWireGroup），
// 保证 docs / authz / mcpx / 宿主模块无论装配顺序如何都已贡献完毕。
var Module = di.Module("fxkit/huma",
	config.Provide[Config]("huma"),
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (huma.API, error) {
			mux, err := di.Invoke[*chi.Mux](i)
			if err != nil {
				return nil, err
			}
			app, _ := di.Invoke[*config.App](i)
			cfg, _ := di.Invoke[*Config](i)
			return NewAPI(mux, app, cfg), nil
		})

		if lc, err := di.Invoke[*di.Lifecycle](i); err == nil && lc != nil {
			lc.AppendPreStartPhase(di.PhaseWireGroup, di.Hook{
				OnStart: func(context.Context) error {
					api, err := di.Invoke[huma.API](i)
					if err != nil {
						return err
					}
					invokeRegistrars(registrarParams{
						API:         api,
						Middlewares: di.GetGroup[MiddlewareRegistrar](i),
						Registrars:  di.GetGroup[Registrar](i),
					})
					return nil
				},
			})
		}
	},
)
