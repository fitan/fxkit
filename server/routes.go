// Package server 承载框架 HTTP 层：内置 CORS、panic 恢复、OpenTelemetry 与请求体日志的单一 chi mux；
// 以及由 Fx 生命周期管理的 [http.Server]。
//
// 其他 fxkit 包与宿主应用通过 "routes" fx group 的 [ProvideRoutes] 贡献路由，
// 通过 "middleware" fx group 的 [ProvideMiddleware] 贡献中间件。见 [Route] 与 [Middleware]。
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
)

// Route 是在共享 chi mux 上的单个挂载点。以 `/` 结尾的 pattern 会自动改写为 `/<pat>*`，
// 以便前缀 handler（例如 Dapr SDK 路由）匹配所有子路径。
//
// Methods 非空时，每条目通过 [chi.Mux.Method] 注册（支持 "/items/{id}" 等路径参数）。
// Methods 为空时使用 [chi.Mux.Handle] 及上述尾部斜杠通配规则。
type Route struct {
	Pattern string
	Handler http.Handler
	Methods []string
}

// Middleware 是标准 net/http 中间件。"middleware" fx group 中的项在 [NewMux] 时按提供顺序挂载。
type Middleware = func(http.Handler) http.Handler

// ProvideRoutes 是模块发布路由的规范方式：
//
//	server.ProvideRoutes(func(svc *MySvc) []server.Route {
//	    return []server.Route{
//	        {Pattern: "/foo", Handler: svc.fooHandler()},
//	    }
//	})
func ProvideRoutes(fn any) fx.Option {
	return fx.Provide(fx.Annotate(fn, fx.ResultTags(`group:"routes,flatten"`)))
}

// PrefixRoute 为前缀 handler 返回 [Route]。pattern 应以 "/" 结尾，
// 以便 [RegisterRoutes] 应用 chi 子树通配符（例如 "/greet.v1.GreetService/"）。
func PrefixRoute(pattern string, h http.Handler) Route {
	if pattern != "" && pattern[len(pattern)-1] != '/' {
		pattern += "/"
	}
	return Route{Pattern: pattern, Handler: h}
}

// ProvideMiddleware 将 chi 中间件发布到共享链。构造函数可依赖任意 Fx 提供的值。
// 多个 provider 之间的顺序不保证，不应依赖顺序作为安全边界（请使用单一组合中间件）。
func ProvideMiddleware(fn any) fx.Option {
	return fx.Provide(fx.Annotate(fn, fx.ResultTags(`group:"middleware"`)))
}

// defaultRoutes 在无其他模块贡献路由时使 "routes" group 可满足（Fx 不允许 grouped 参数使用 optional:"true"）。
func defaultRoutes() []Route { return nil }

func defaultMiddleware() Middleware {
	return func(next http.Handler) http.Handler { return next }
}

// 保留 chi import 引用，避免仅使用 [Route] 与 [Middleware] 的最小构建触发 go vet 警告。
var _ = chi.NewRouter
