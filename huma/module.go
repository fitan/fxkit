// Package huma 在共享 chi mux 上装配 [danielgtaylor/huma/v2]，handler 感知 fxerrors。
package huma

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

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
//
// 支持的形式：
//
//	Registrar                  直接发布
//	func(api huma.API)         适配为 FuncRegistrar
//	func() Registrar           装配期直接调用
//	func(di.Injector) Registrar
//	func(*Service) Registrar   延迟到 PreStart 解析依赖后调用（最常见写法）
//
// 无法识别的签名会 panic —— 静默丢弃会导致「registrar 写好了但请求仍 404」。
func ProvideRegistrar(fn any) di.Option {
	return provideLazyGroupItem[Registrar](
		"huma.ProvideRegistrar: ",
		fn,
		func(v any) (Registrar, bool) {
			switch f := v.(type) {
			case Registrar:
				return f, true
			case func(api huma.API):
				return FuncRegistrar(f), true
			}
			return nil, false
		},
	)
}

// ProvideMiddleware 将一个 [MiddlewareRegistrar] 发布到容器 group。
// 支持的签名与 [ProvideRegistrar] 相同（含 func(*Service) MiddlewareRegistrar）。
func ProvideMiddleware(fn any) di.Option {
	return provideLazyGroupItem[MiddlewareRegistrar](
		"huma.ProvideMiddleware: ",
		fn,
		func(v any) (MiddlewareRegistrar, bool) {
			switch f := v.(type) {
			case MiddlewareRegistrar:
				return f, true
			case func(api huma.API) Middleware:
				return MiddlewareFunc(f), true
			}
			return nil, false
		},
	)
}

// provideLazyGroupItem 是 Provide* 系列的统一实现。
//
// 形如 func(*Service) T 的构造函数在装配期无法调用（依赖尚未装配完），
// 因此包成「PreStart 时解析依赖再调用」的延迟项。无法识别的签名一律 panic，
// 以免出现「模块看似装配成功、运行期却缺注册项」的静默失败。
func provideLazyGroupItem[T any](
	caller string,
	fn any,
	convert func(any) (T, bool),
) di.Option {
	return func(i di.Injector) {
		if fn == nil {
			return
		}
		if v, ok := convert(fn); ok {
			di.ProvideGroupItem(i, v)
			return
		}
		switch f := fn.(type) {
		case func() T:
			di.ProvideGroupItem(i, f())
			return
		case func(di.Injector) T:
			di.ProvideGroupItem(i, f(i))
			return
		}

		// 任意 func(dep...) T / func(dep...) (T, error)：延迟到 PreStart 装配。
		val := reflect.ValueOf(fn)
		if val.Kind() != reflect.Func {
			panic(caller + "unsupported argument " + reflect.TypeOf(fn).String() +
				"; want a registrar value or a constructor returning one")
		}
		if err := di.ValidateConstructor(val.Type()); err != nil {
			panic(caller + err.Error())
		}

		lc, err := di.Invoke[*di.Lifecycle](i)
		if err != nil || lc == nil {
			panic(caller + "constructor form requires *di.Lifecycle in the graph")
		}
		// 必须早于 huma 的安装阶段（PhaseWireGroup）：安装时要读完整的 group。
		lc.AppendPreStartPhase(di.PhaseResolveContributors, di.Hook{
			OnStart: func(context.Context) error {
				out, callErr := di.CallConstructor(i, fn)
				if callErr != nil {
					return fmt.Errorf("%s%w", caller, callErr)
				}
				// 可能返回 T 本身，也可能返回可转换的形式（如 func(api huma.API)）。
				if v, ok := out.(T); ok {
					di.ProvideGroupItem(i, v)
					return nil
				}
				if v, ok := convert(out); ok {
					di.ProvideGroupItem(i, v)
					return nil
				}
				return fmt.Errorf("%sconstructor returned %T, want %T", caller, out, out)
			},
		})
	}
}

// Module 提供共享 [huma.API]，挂载所有 [MiddlewareRegistrar]，并执行所有 [Registrar] hook。
//
// huma 在 RegisterOperation 时 **快照** api.Middlewares()，因此中间件与 registrar
// 都必须在「所有模块装配完成之后」一次性安装，否则后装配模块的 registrar 会被漏掉、
// 后注册的中间件不会作用到已注册的 operation。
// 这里把安装动作放到 Lifecycle.PreStart（PhaseWireGroup），
// 保证 docs / authz / mcpx / 宿主模块无论装配顺序如何都已贡献完毕。
var Module = di.Module("fxkit/huma",
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (huma.API, error) {
			mux, err := di.Invoke[*chi.Mux](i)
			if err != nil {
				return nil, err
			}
			app, _ := di.Invoke[*config.App](i)
			return NewAPI(mux, app), nil
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
