// Package huma 在共享 chi mux 上装配 [danielgtaylor/huma/v2]，handler 感知 fxerrors。
package huma

import (
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

// NewAPI 在共享 chi mux 上构建 Huma API，使用 app 元数据作为 OpenAPI 标题。
func NewAPI(mux *chi.Mux, app *config.App) huma.API {
	appName := "app"
	if app != nil && app.Name != "" {
		appName = app.Name
	}
	conf := huma.DefaultConfig(appName, "1.0.0")
	conf.DocsPath = ""
	conf.OpenAPIPath = "/huma/openapi"
	return humachi.New(mux, conf)
}

// ProvideRegistrar 将 Registrar 发布到容器 group。
func ProvideRegistrar(fn any) di.Option {
	return func(i di.Injector) {
		switch f := fn.(type) {
		case Registrar:
			di.ProvideGroupItem(i, f)
		case func(api huma.API):
			di.ProvideGroupItem[Registrar](i, FuncRegistrar(f))
		case func() Registrar:
			di.ProvideGroupItem(i, f())
		case func(di.Injector) Registrar:
			di.ProvideGroupItem(i, f(i))
		}
	}
}

// ProvideMiddleware 将一个 [MiddlewareRegistrar] 发布到容器 group。
func ProvideMiddleware(fn any) di.Option {
	return func(i di.Injector) {
		switch f := fn.(type) {
		case MiddlewareRegistrar:
			di.ProvideGroupItem(i, f)
		case func(api huma.API) Middleware:
			di.ProvideGroupItem[MiddlewareRegistrar](i, MiddlewareFunc(f))
		case func() MiddlewareRegistrar:
			di.ProvideGroupItem(i, f())
		case func(di.Injector) MiddlewareRegistrar:
			di.ProvideGroupItem(i, f(i))
		}
	}
}

// Module 提供共享 [huma.API]，挂载所有 [MiddlewareRegistrar]，并执行所有 [Registrar] hook。
var Module = di.Module("fxkit/huma",
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (huma.API, error) {
			mux, err := di.Invoke[*chi.Mux](i)
			if err != nil {
				return nil, err
			}
			app, _ := di.Invoke[*config.App](i)
			api := NewAPI(mux, app)

			mws := di.GetGroup[MiddlewareRegistrar](i)
			regs := di.GetGroup[Registrar](i)
			invokeRegistrars(registrarParams{
				API:         api,
				Middlewares: mws,
				Registrars:  regs,
			})
			return api, nil
		})
	},
)
