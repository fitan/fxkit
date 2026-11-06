// Package server 承载框架 HTTP 层：内置 CORS、panic 恢复、OpenTelemetry 与请求体日志的单一 chi mux；
// 以及由生命周期管理的 [http.Server]。
//
// 其他包与宿主应用通过 [ProvideRoutes] 贡献路由，
// 通过 [ProvideMiddleware] 贡献中间件。见 [Route] 与 [Middleware]。
package server

import (
	"errors"
	"net/http"

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
	// MaxBodyBytes 为该路由单独限制请求体大小。
	//
	//  0  使用 [Config.MaxBodyBytes]（默认 1 MiB，与 Huma 路由一致）
	// <0  不限制（例如文件上传路由）
	// >0  使用该值
	MaxBodyBytes int64
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
// limitRequestBody 把请求体限制在 max 字节；max <= 0 表示不限制。
//
// 超限时 Read 返回 *http.MaxBytesError（用 [IsBodyTooLarge] 判断），
// 调用方应回 413。
func limitRequestBody(h http.Handler, max int64) http.Handler {
	if max <= 0 {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		h.ServeHTTP(w, r)
	})
}

// IsBodyTooLarge 报告 err 是否由请求体超过 [Config.MaxBodyBytes] /
// [Route.MaxBodyBytes] 引起，供裸 chi handler 回 413。
func IsBodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// ProvideRoutes 发布若干 [Route]。需要依赖时用 [ProvideRoutesLazy]。
func ProvideRoutes(routes ...Route) di.Option {
	return func(i di.Injector) {
		if len(routes) == 0 {
			return
		}
		di.ProvideGroupItems(i, routes...)
	}
}

// ProvideRoutesLazy 在 PreStart 求值（可访问完整依赖图）。
func ProvideRoutesLazy(fn func(di.Injector) ([]Route, error)) di.Option {
	return func(i di.Injector) {
		di.ProvideLazyE[[]Route](i, fn)
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

// ProvideMiddleware 发布若干 chi 中间件。需要依赖时用 [ProvideMiddlewareLazy]。
func ProvideMiddleware(mws ...Middleware) di.Option {
	return func(i di.Injector) {
		for _, mw := range mws {
			if mw != nil {
				di.ProvideGroupItem(i, mw)
			}
		}
	}
}

// ProvideMiddlewareLazy 在 PreStart 求值（可访问完整依赖图）。
func ProvideMiddlewareLazy(fn func(di.Injector) (Middleware, error)) di.Option {
	return func(i di.Injector) { di.ProvideLazyE[Middleware](i, fn) }
}
