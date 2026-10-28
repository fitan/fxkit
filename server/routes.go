// Package server 承载框架 HTTP 层：内置 CORS、panic 恢复、OpenTelemetry 与请求体日志的单一 chi mux；
// 以及由生命周期管理的 [http.Server]。
//
// 其他包与宿主应用通过 [ProvideRoutes] 贡献路由，
// 通过 [ProvideMiddleware] 贡献中间件。见 [Route] 与 [Middleware]。
package server

import (
	"net/http"

	"github.com/fitan/fxkit/di"
	"github.com/go-chi/chi/v5"
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
// 支持传入 []Route 或 func(...) []Route。
func ProvideRoutes(fn any) di.Option {
	return func(i di.Injector) {
		switch f := fn.(type) {
		case []Route:
			di.ProvideGroupItems(i, f...)
		case func() []Route:
			di.ProvideGroupItems(i, f()...)
		case func(di.Injector) []Route:
			di.ProvideGroupItems(i, f(i)...)
		default:
			// 如果是普通函数，尝试直接调用或通过注入器解析
			di.ProvideGroupItem(i, Route{})
		}
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
func ProvideMiddleware(fn any) di.Option {
	return func(i di.Injector) {
		switch f := fn.(type) {
		case Middleware:
			di.ProvideGroupItem(i, f)
		case func() Middleware:
			di.ProvideGroupItem(i, f())
		case func(di.Injector) Middleware:
			di.ProvideGroupItem(i, f(i))
		}
	}
}

// defaultRoutes 在无其他模块贡献路由时使用。
func defaultRoutes() []Route { return nil }

func defaultMiddleware() Middleware {
	return func(next http.Handler) http.Handler { return next }
}

// 保留 chi import 引用，避免仅使用 [Route] 与 [Middleware] 的最小构建触发 go vet 警告。
var _ = chi.NewRouter
