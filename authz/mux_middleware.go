package authz

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
)

// MuxMiddlewareConfig 配置 chi 鉴权中间件。
//
// 它覆盖所有经过 mux 的请求，包括 Huma、裸 chi、/docs、/mcp，
// 并把 Subject 写入 request context，供 [SubjectFromContext] 使用。
type MuxMiddlewareConfig struct {
	Enabled       bool
	DevHeaderUser bool
	Validator     TokenValidator
	Enforcer      *Enforcer
	SubjectFunc   SubjectFunc
	// PublicPaths 完全放行的路径（精确或前缀），用于 /public/* 这类匿名端点。
	PublicPaths []string
	// DevOrgID 是 DevHeaderUser 模式下 X-Org 缺失时的默认租户。
	DevOrgID string
	// AutoBindBootstrap 在主体于请求 domain 尚无角色时绑定 BootstrapRole。
	AutoBindBootstrap bool
	BootstrapRole     string
}

// NewMuxMiddleware 返回一个 net/http 中间件：认证 + Casbin 授权。
//
// 判定顺序：公开路径 → 解析身份（401）→ Casbin（403/500）→ 写入 Subject。
// Casbin 关闭时，登录即可。
func NewMuxMiddleware(cfg MuxMiddlewareConfig) func(http.Handler) http.Handler {
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

			if cfg.AutoBindBootstrap && cfg.Enforcer != nil && cfg.Enforcer.Enabled() {
				_ = cfg.Enforcer.AutoBindOnce(r.Context(), subj.ID, cfg.BootstrapRole, subj.OrgID)
			}

			switch authorize(r.Context(), subj, r.Method, path, cfg.Enforcer) {
			case decisionInternal:
				writeAuthzError(w, http.StatusInternalServerError, "internal error")
				return
			case decisionForbidden:
				writeAuthzError(w, http.StatusForbidden, "forbidden")
				return
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
				if s.OrgID == "" {
					s.OrgID = strings.TrimSpace(cfg.DevOrgID)
				}
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

type unauthorizedError struct{}

func (unauthorizedError) Error() string { return "missing or invalid credentials" }

var errUnauthorized unauthorizedError

// provideMuxMiddleware 在装配期解析 config/validator/enforcer。
func provideMuxMiddleware(inj di.Injector) (server.Middleware, error) {
	cfgPtr := mustResolve[*Config](inj, "*Config")
	validator := mustResolve[TokenValidator](inj, "TokenValidator")
	enforcer := mustResolve[*Enforcer](inj, "*Enforcer")

	cfg := *cfgPtr
	if cfg.Enabled && cfg.DevHeaderUser {
		slog.Warn("auth.dev_header_user enabled: X-User bypasses JWT — disable in production")
	}
	if cfg.Enabled && cfg.Casbin.AutoBindBootstrap {
		slog.Warn("auth.casbin.auto_bind_bootstrap enabled: unbound subjects get bootstrap role — disable in production")
	}
	return NewMuxMiddleware(MuxMiddlewareConfig{
		Enabled:           cfg.Enabled,
		DevHeaderUser:     cfg.DevHeaderUser,
		Validator:         validator,
		Enforcer:          enforcer,
		SubjectFunc:       DefaultDevSubjectFunc,
		PublicPaths:       cfg.PublicPaths,
		DevOrgID:          cfg.DevOrgID,
		AutoBindBootstrap: cfg.Casbin.AutoBindBootstrap,
		BootstrapRole:     strings.TrimSpace(cfg.Casbin.BootstrapRole),
	}), nil
}
