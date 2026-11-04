// Package server 承载框架 HTTP 层：内置 CORS、panic 恢复、OpenTelemetry 与请求体日志的单一 chi mux；
// 以及由生命周期管理的 [http.Server]。
//
// 其他包与宿主应用通过 [ProvideRoutes] 贡献路由，
// 通过 [ProvideMiddleware] 贡献中间件。见 [Route] 与 [Middleware]。
package server

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	"github.com/fitan/fxkit/di"
)

// Route 是在共享 chi mux 上的单个挂载点。以 `/` 结尾的 pattern 会自动改写为 `/<pat>*`，
// 以便前缀 handler 匹配所有子路径。
//
// Methods 非空时，每条目通过 [chi.Mux.Method] 注册（支持 "/items/{id}" 等路径参数）。
// Methods 为空时使用 [chi.Mux.Handle] 及上述尾部斜杠通配规则。
type Route struct {
	Pattern string
	Handler http.Handler
	Methods []string
}

// Middleware 是标准 net/http 中间件。在 [NewMux] 时按提供顺序挂载。
type Middleware = func(http.Handler) http.Handler

// ProvideRoutes 是模块发布路由的规范方式。
//
// 支持的形式：
//
//	[]Route                     直接发布
//	Route                       发布单条
//	func() []Route              装配期调用
//	func(di.Injector) []Route   装配期调用
//	func(*Service) []Route      延迟到 PreStart 解析依赖后调用
//
// 无法识别的签名会 panic。此前的实现会静默塞入 Route{} 空路由，
// 直接导致 chi 在注册时报 "routing pattern must begin with '/'"。
func ProvideRoutes(fn any) di.Option {
	return func(i di.Injector) {
		if fn == nil {
			return
		}
		switch f := fn.(type) {
		case []Route:
			di.ProvideGroupItems(i, f...)
			return
		case Route:
			di.ProvideGroupItem(i, f)
			return
		case func() []Route:
			di.ProvideGroupItems(i, f()...)
			return
		case func(di.Injector) []Route:
			di.ProvideGroupItems(i, f(i)...)
			return
		}

		// 任意 func(dep...) []Route / func(dep...) ([]Route, error)：延迟装配。
		val := reflect.ValueOf(fn)
		if val.Kind() != reflect.Func {
			panic("server.ProvideRoutes: unsupported argument " + reflect.TypeOf(fn).String() +
				"; want []Route or a constructor returning it")
		}
		if err := di.ValidateConstructor(val.Type()); err != nil {
			panic("server.ProvideRoutes: " + err.Error())
		}
		lc, err := di.Invoke[*di.Lifecycle](i)
		if err != nil || lc == nil {
			panic("server.ProvideRoutes: constructor form requires *di.Lifecycle in the graph")
		}
		lc.AppendPreStartPhase(di.PhaseResolveContributors, di.Hook{
			OnStart: func(context.Context) error {
				out, callErr := di.CallConstructor(i, fn)
				if callErr != nil {
					return fmt.Errorf("server.ProvideRoutes: %w", callErr)
				}
				routes, ok := out.([]Route)
				if !ok {
					return fmt.Errorf("server.ProvideRoutes: constructor returned %T, want []server.Route", out)
				}
				di.ProvideGroupItems(i, routes...)
				return nil
			},
		})
	}
}

// PrefixRoute 为前缀 handler 返回 [Route]。pattern 应以 "/" 结尾，
// 以便 [RegisterRoutes] 应用 chi 子树通配符（例如 "/greet.v1.GreetService/"）。
func PrefixRoute(pattern string, h http.Handler) Route {
	if pattern != "" && pattern[len(pattern)-1] != '/' {
		pattern += "/"
	}
	return Route{Pattern: pattern, Handler: h}
}

// ProvideMiddleware 将 chi 中间件发布到共享链。
//
// 支持的形式：Middleware / func() Middleware / func(di.Injector) Middleware /
// func(*Service) Middleware。无法识别的签名会 panic。
func ProvideMiddleware(fn any) di.Option {
	return func(i di.Injector) {
		if fn == nil {
			return
		}
		switch f := fn.(type) {
		case Middleware:
			di.ProvideGroupItem(i, f)
			return
		case func() Middleware:
			di.ProvideGroupItem(i, f())
			return
		case func(di.Injector) Middleware:
			di.ProvideGroupItem(i, f(i))
			return
		}
		val := reflect.ValueOf(fn)
		if val.Kind() != reflect.Func {
			panic("server.ProvideMiddleware: unsupported argument " + reflect.TypeOf(fn).String())
		}
		if err := di.ValidateConstructor(val.Type()); err != nil {
			panic("server.ProvideMiddleware: " + err.Error())
		}
		lc, err := di.Invoke[*di.Lifecycle](i)
		if err != nil || lc == nil {
			panic("server.ProvideMiddleware: constructor form requires *di.Lifecycle in the graph")
		}
		lc.AppendPreStartPhase(di.PhaseResolveContributors, di.Hook{
			OnStart: func(context.Context) error {
				out, callErr := di.CallConstructor(i, fn)
				if callErr != nil {
					return fmt.Errorf("server.ProvideMiddleware: %w", callErr)
				}
				mw, ok := out.(Middleware)
				if !ok {
					return fmt.Errorf("server.ProvideMiddleware: constructor returned %T, want server.Middleware", out)
				}
				di.ProvideGroupItem(i, mw)
				return nil
			},
		})
	}
}
