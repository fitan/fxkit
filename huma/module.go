// Package huma 在共享 chi mux 上装配 [danielgtaylor/huma/v2]，handler 感知 fxerrors。
// 模块通过 [ProvideRegistrar] 或接受 [huma.API] 的 fx.Invoke 构造函数注册 operation。
package huma

import (
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/fitan/fxkit/config"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
)

// Registrar 向共享 API 实例挂载一个或多个 Huma operation。
type Registrar interface {
	Register(api huma.API)
}

// FuncRegistrar 将普通 func 适配为通过闭包注册的模块。
type FuncRegistrar func(api huma.API)

func (f FuncRegistrar) Register(api huma.API) { f(api) }

// Middleware 是 Huma 中间件签名（见 [huma.API.UseMiddleware]）。用于在 REST/Huma 路径上执行
// 认证、鉴权等横切逻辑。
type Middleware = func(ctx huma.Context, next func(huma.Context))

// MiddlewareRegistrar 在拿到共享 [huma.API] 后构造一个 [Middleware]。
// 需要 API 引用是因为中间件通常用 [huma.WriteErr] 写出错误响应。
type MiddlewareRegistrar interface {
	Middleware(api huma.API) Middleware
}

// MiddlewareFunc 将普通 func 适配为 [MiddlewareRegistrar]。
type MiddlewareFunc func(api huma.API) Middleware

func (f MiddlewareFunc) Middleware(api huma.API) Middleware { return f(api) }

type registrarParams struct {
	fx.In
	API         huma.API
	Middlewares []MiddlewareRegistrar `group:"huma_middlewares"`
	Registrars  []Registrar           `group:"huma_registrars"`
}

func invokeRegistrars(p registrarParams) {
	// 中间件先于 operation 挂载；[huma.API.UseMiddleware] 对所有 operation 生效，与注册顺序无关。
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

// NewAPI 在共享 chi mux 上构建 Huma API，使用 app 元数据作为 OpenAPI 标题。
func NewAPI(mux *chi.Mux, cfg *config.Config) huma.API {
	app := cfg.Get().App
	title := app.Name
	if title == "" {
		title = "API"
	}
	conf := huma.DefaultConfig(title, "1.0.0")
	// 宿主应用自行提供 Scalar/docs（见 fxkit/docs）。
	conf.DocsPath = ""
	conf.OpenAPIPath = "/huma/openapi.json"
	return humachi.New(mux, conf)
}

// ProvideRegistrar 将启动 hook 发布到 "huma_registrars" fx group。
// 构造函数须返回 [Registrar]（常为 [FuncRegistrar]）。
//
//	func RegisterDemo(svc *demo.Service) fxhuma.Registrar {
//	    return fxhuma.FuncRegistrar(func(api huma.API) {
//	        fxhuma.Register(api, huma.Operation{...}, handler)
//	    })
//	}
//
//	var Module = fx.Options(
//	    huma.ProvideRegistrar(RegisterDemo),
//	)
func ProvideRegistrar(fn any) fx.Option {
	return fx.Provide(fx.Annotate(fn, fx.As(new(Registrar)), fx.ResultTags(`group:"huma_registrars"`)))
}

func defaultRegistrars() []Registrar { return nil }

// ProvideMiddleware 将一个 [MiddlewareRegistrar] 发布到 "huma_middlewares" fx group，
// 由 [Module] 在启动时通过 [huma.API.UseMiddleware] 挂载。构造函数须返回 [MiddlewareRegistrar]
//（常为 [MiddlewareFunc]），可依赖任意 Fx 提供的值。
//
//	func provideAuthMiddleware(p Params) fxhuma.MiddlewareRegistrar { ... }
//
//	var Module = fx.Options(fxhuma.ProvideMiddleware(provideAuthMiddleware))
func ProvideMiddleware(fn any) fx.Option {
	return fx.Provide(fx.Annotate(fn, fx.As(new(MiddlewareRegistrar)), fx.ResultTags(`group:"huma_middlewares"`)))
}

func defaultMiddlewares() []MiddlewareRegistrar { return nil }

// Module 提供共享 [huma.API]，挂载所有 [MiddlewareRegistrar]，并执行所有 [Registrar] hook。
var Module = fx.Module("fxkit/huma",
	fx.Provide(NewAPI),
	fx.Provide(fx.Annotate(defaultRegistrars, fx.ResultTags(`group:"huma_registrars,flatten"`))),
	fx.Provide(fx.Annotate(defaultMiddlewares, fx.ResultTags(`group:"huma_middlewares,flatten"`))),
	fx.Invoke(invokeRegistrars),
)
