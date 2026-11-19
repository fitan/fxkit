// Package server 承载框架 HTTP 层：内置 CORS、panic 恢复、OpenTelemetry 与请求体日志的单一 chi mux；
// 以及由生命周期管理的 [http.Server]。
//
// 其他包与宿主应用通过 [ProvideRoutes] 贡献路由，
// 通过 [ProvideMiddleware] 贡献中间件。见 [Route] 与 [Middleware]。
package server

import (
	"context"
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
	// Pattern 是 chi 路由模板，必须以 / 开头；Methods 为空且以 / 结尾时会追加 * 以匹配子路径。
	Pattern string
	// Handler 是路由处理器；nil 会让启动失败。
	Handler http.Handler
	// Methods 为空时用 [chi.Mux.Handle] 注册前缀 handler；非空时逐条通过 [chi.Mux.Method] 注册。
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
		h.ServeHTTP(&rejectionRecorder{ResponseWriter: w, ctx: r.Context()}, r)
	})
}

// rejectionRecorder 观察 handler 回写的 413，用于请求体超限计数。
// Unwrap/Flush 保留底层 writer 能力，避免影响流式响应。
type rejectionRecorder struct {
	http.ResponseWriter
	// ctx 是请求 context，用于记录拒绝指标。
	ctx context.Context
	// counted 标记 413 是否已计数，避免重复记录。
	counted bool
}

// WriteHeader 透传状态码；首次出现 413 时计入 body_too_large 拒绝。
func (w *rejectionRecorder) WriteHeader(code int) {
	if code == http.StatusRequestEntityTooLarge && !w.counted {
		w.counted = true
		recordRejection(w.ctx, "body_too_large")
	}
	w.ResponseWriter.WriteHeader(code)
}

// Flush 在底层 writer 支持 http.Flusher 时透传，保证 SSE 等流式响应不被缓冲。
func (w *rejectionRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 返回底层 ResponseWriter，供 http.ResponseController 透传能力。
func (w *rejectionRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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
		if fn == nil {
			return
		}
		lc, err := di.Invoke[*di.Lifecycle](i)
		if err != nil || lc == nil {
			return
		}
		lc.AppendPreStartPhase(di.PhaseResolveContributors, di.Hook{
			OnStart: func(context.Context) error {
				routes, err := fn(i)
				if err != nil {
					return err
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
