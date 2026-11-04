package authz

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
)

// MuxMiddlewareConfig 配置 mux 层鉴权。
//
// 与 Huma 层中间件的区别：它覆盖**所有**经过 chi/mux 的请求，包括裸 chi 路由、
// /docs、/mcp 等；并把 Subject 写入 request context，供 [SubjectFromContext] 使用。
type MuxMiddlewareConfig struct {
	Enabled          bool
	DevHeaderUser    bool
	DenyUnregistered bool
	Validator        TokenValidator
	Enforcer         *Enforcer
	// Routes 惰性返回已登记的 HTTPRoute（在首次请求时读取 group，确保完整）。
	Routes      func() []HTTPRoute
	SubjectFunc SubjectFunc
	// PublicPaths 完全放行的路径（精确或前缀），用于 /public/* 这类匿名端点。
	PublicPaths []string
}

// NewMuxMiddleware 返回一个 net/http 中间件：认证 + Casbin 授权。
//
// 判定顺序：公开路径 → 解析身份（401）→ Casbin（403/500）→ 写入 Subject。
// Casbin 关闭时退化为 HTTPRoute.Permission 校验（与 Huma 层一致）。
func NewMuxMiddleware(cfg MuxMiddlewareConfig) func(http.Handler) http.Handler {
	var (
		idxOnce sync.Once
		idx     routeIndex
	)
	index := func() routeIndex {
		idxOnce.Do(func() {
			if cfg.Routes == nil {
				return
			}
			idx = indexRoutes(cfg.Routes())
		})
		return idx
	}

	return func(next http.Handler) http.Handler {
		if !cfg.Enabled {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := normalizeRequestPath(r.URL.Path)
			if skipAuthzPath(path) || isPublicPath(path, cfg.PublicPaths) {
				next.ServeHTTP(w, r)
				return
			}

			subj, err := resolveRequestSubject(r, cfg)
			if err != nil || subj.ID == "" {
				writeAuthzError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			if cfg.Enforcer != nil && cfg.Enforcer.Enabled() {
				allowed, enfErr := cfg.Enforcer.Enforce(r.Context(), EnforceInput{
					Sub: subj.ID,
					Obj: path,
					Act: r.Method,
				})
				if enfErr != nil {
					writeAuthzError(w, http.StatusInternalServerError, "internal error")
					return
				}
				if !allowed {
					writeAuthzError(w, http.StatusForbidden, "forbidden")
					return
				}
			} else {
				route, ok := index().find(r.Method, path)
				if ok && route.Permission != "" && !subj.Has(route.Permission) {
					writeAuthzError(w, http.StatusForbidden, "forbidden")
					return
				}
				if !ok && cfg.DenyUnregistered {
					writeAuthzError(w, http.StatusForbidden, "forbidden")
					return
				}
			}

			// 让裸 chi handler 也能通过 authz.SubjectFromContext 拿到身份。
			next.ServeHTTP(w, r.WithContext(WithSubject(r.Context(), subj)))
		})
	}
}

func resolveRequestSubject(r *http.Request, cfg MuxMiddlewareConfig) (Subject, error) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") {
		if cfg.Validator != nil {
			raw := strings.TrimSpace(auth[7:])
			if raw == "" {
				return Subject{}, errUnauthorized
			}
			return cfg.Validator.Validate(r.Context(), raw)
		}
	}
	if cfg.DevHeaderUser {
		if u := strings.TrimSpace(r.Header.Get("X-User")); u != "" {
			fn := cfg.SubjectFunc
			if fn == nil {
				fn = DefaultDevSubjectFunc
			}
			if s := fn(r.Context(), r.Header); s.ID != "" {
				return s, nil
			}
		}
	}
	return Subject{}, errUnauthorized
}

func isPublicPath(path string, public []string) bool {
	for _, p := range public {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		pp := normalizeRequestPath(p)
		if path == pp || strings.HasPrefix(path, pp+"/") {
			return true
		}
	}
	return false
}

func writeAuthzError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    http.StatusText(status),
		"message": msg,
	})
}

// provideMuxMiddleware 是 [authz.Module] 在 mux 模式下的接线。
//
// 注意：这里在装配期解析 config/validator/enforcer（与 Huma 层在 PreStart 解析等价，
// 都是启动期一次性成本）；HTTPRoute 则在**首次请求**时读取，确保包含全部
// 构造函数形式登记的路径。
func provideMuxMiddleware(inj di.Injector) server.Middleware {
	cfgPtr, _ := di.Invoke[*Config](inj)
	validator, _ := di.Invoke[TokenValidator](inj)
	enforcer, _ := di.Invoke[*Enforcer](inj)

	cfg := Config{}
	if cfgPtr != nil {
		cfg = *cfgPtr
	}

	var (
		once   sync.Once
		cached []HTTPRoute
	)
	routes := func() []HTTPRoute {
		once.Do(func() {
			cached = di.GetGroup[HTTPRoute](inj)
		})
		return cached
	}

	return NewMuxMiddleware(MuxMiddlewareConfig{
		Enabled:          cfg.Enabled,
		DevHeaderUser:    cfg.DevHeaderUser,
		DenyUnregistered: cfg.DenyUnregistered,
		Validator:        validator,
		Enforcer:         enforcer,
		Routes:           routes,
		SubjectFunc:      DefaultDevSubjectFunc,
		PublicPaths:      cfg.PublicPaths,
	})
}
