package system

import (
	"encoding/json"
	"net/http"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/server"
	"github.com/go-chi/chi/v5"
)

// chiHeader 演示 server.ProvideMiddleware：给所有响应加一个纯 chi 层标记。
func chiHeader() server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Fxkit-Chi", "1")
			next.ServeHTTP(w, r)
		})
	}
}

// chiRoutes 演示三类裸 chi 路由：
//   - 普通 GET；
//   - 带 path 参数的 fxerrors 错误输出；
//   - PrefixRoute 前缀挂载。
func chiRoutes() []server.Route {
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"echo": r.URL.Query().Get("msg"),
			"via":  "chi",
		})
	})
	errors := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := chi.URLParam(r, "kind")
		switch kind {
		case "notfound":
			fxerrors.WriteError(w, r, fxerrors.NotFound("demo", "missing"))
		case "conflict":
			fxerrors.WriteError(w, r, fxerrors.Conflict("already exists"))
		case "validation":
			fxerrors.WriteError(w, r, fxerrors.Validation(map[string]string{"field": "required"}))
		case "boom":
			fxerrors.WriteError(w, r, fxerrors.Internal("secret cause"))
		default:
			fxerrors.WriteError(w, r, fxerrors.BadRequest("unknown kind %s", kind))
		}
	})
	tree := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"path": r.URL.Path, "via": "PrefixRoute"})
	})
	return []server.Route{
		{Pattern: "/chi/echo", Methods: []string{http.MethodGet}, Handler: echo},
		{Pattern: "/chi/errors/{kind}", Methods: []string{http.MethodGet}, Handler: errors},
		server.PrefixRoute("/chi/tree/", tree),
	}
}
