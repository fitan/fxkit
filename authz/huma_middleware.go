package authz

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	fxhuma "github.com/fitan/fxkit/huma"
	"go.uber.org/fx"
)

type humaMiddlewareParams struct {
	fx.In
	Config    *Config
	Validator TokenValidator `optional:"true"`
	Enforcer  *Enforcer      `optional:"true"`
	Routes    []HTTPRoute    `group:"authz_http_routes"`
}

func provideHumaMiddleware(p humaMiddlewareParams) fxhuma.MiddlewareRegistrar {
	cfg := Config{}
	if p.Config != nil {
		cfg = *p.Config
	}
	if cfg.Enabled && cfg.DevHeaderUser {
		slog.Warn("auth.dev_header_user enabled: X-User bypasses JWT — disable in production")
	}
	if cfg.Enabled && !cfg.DenyUnregistered && (p.Enforcer == nil || !p.Enforcer.Enabled()) {
		slog.Warn("auth.deny_unregistered=false: Huma ops still require login; unlisted ops are login-only (not anonymous)")
	}
	role := strings.TrimSpace(cfg.Casbin.BootstrapRole)
	if cfg.Casbin.AutoBindBootstrap {
		slog.Warn("auth.casbin.auto_bind_bootstrap enabled: unbound subjects get bootstrap role — disable in production")
	}
	return NewHumaMiddleware(HumaMiddlewareConfig{
		Enabled:           cfg.Enabled,
		DevHeaderUser:     cfg.DevHeaderUser,
		DenyUnregistered:  cfg.DenyUnregistered,
		AutoBindBootstrap: cfg.Casbin.AutoBindBootstrap,
		BootstrapRole:     role,
		Validator:         p.Validator,
		Enforcer:          p.Enforcer,
		Routes:            p.Routes,
		SubjectFunc:       DefaultDevSubjectFunc,
	})
}

// HumaMiddlewareConfig configures [NewHumaMiddleware].
type HumaMiddlewareConfig struct {
	Enabled           bool
	DevHeaderUser     bool
	DenyUnregistered  bool
	AutoBindBootstrap bool
	BootstrapRole     string
	Validator         TokenValidator
	Enforcer          *Enforcer
	Routes            []HTTPRoute
	SubjectFunc       SubjectFunc
}

// NewHumaMiddleware 对所有 Huma operation 校验身份（Bearer JWT 或开发头），再做授权。
// Casbin 启用时：Enforce(sub, requestPath, METHOD)。
// Casbin 关闭时：若路由设置了 Permission，则检查 Subject.Has；否则登录即可。
// 未在 HTTPRoute 表中的 operation 仍需登录（不再匿名放行）。DenyUnregistered
// 为 true 且 Casbin 关闭时，未登记路由 403。
// 将 huma.Operation.Metadata[OpPublic]=true 可跳过鉴权。
func NewHumaMiddleware(cfg HumaMiddlewareConfig) fxhuma.MiddlewareRegistrar {
	subjectFn := cfg.SubjectFunc
	if subjectFn == nil {
		subjectFn = DefaultDevSubjectFunc
	}
	idx := indexRoutes(cfg.Routes)
	enf := cfg.Enforcer

	return fxhuma.MiddlewareFunc(func(api huma.API) fxhuma.Middleware {
		if !cfg.Enabled {
			return nil
		}
		return func(ctx huma.Context, next func(huma.Context)) {
			if skipAuthzPath(ctx.URL().Path) || isPublicOperation(ctx.Operation()) {
				next(ctx)
				return
			}

			method := ctx.Method()
			path := normalizeRequestPath(ctx.URL().Path)

			subj, err := resolveSubject(ctx, cfg, subjectFn)
			if err != nil || subj.ID == "" {
				_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "unauthorized")
				return
			}

			route, ok := idx.find(method, path)
			if !ok {
				if op := ctx.Operation(); op != nil && op.Path != "" {
					route, ok = idx.find(method, normalizeRequestPath(op.Path))
				}
			}

			if enf != nil && enf.Enabled() {
				if cfg.AutoBindBootstrap {
					maybeAutoBindBootstrap(ctx.Context(), enf, subj.ID, cfg.BootstrapRole)
				}
				allowed, enfErr := enf.Enforce(ctx.Context(), EnforceInput{
					Sub: subj.ID,
					Obj: path,
					Act: method,
				})
				if enfErr != nil {
					slog.Error("auth casbin enforce failed", "error", enfErr, "sub", subj.ID, "obj", path, "act", method)
					_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "internal error")
					return
				}
				if !allowed {
					_ = huma.WriteErr(api, ctx, http.StatusForbidden, "forbidden")
					return
				}
			} else if ok && route.Permission != "" && !subj.Has(route.Permission) {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, "forbidden")
				return
			} else if !ok && cfg.DenyUnregistered {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, "forbidden")
				return
			}
			next(huma.WithValue(ctx, subjectCtxKey{}, subj))
		}
	})
}

// OpPublic is the huma.Operation.Metadata key that skips authz for that operation.
const OpPublic = "authz.public"

func isPublicOperation(op *huma.Operation) bool {
	if op == nil || op.Metadata == nil {
		return false
	}
	v, ok := op.Metadata[OpPublic]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func resolveSubject(ctx huma.Context, cfg HumaMiddlewareConfig, subjectFn SubjectFunc) (Subject, error) {
	auth := strings.TrimSpace(ctx.Header("Authorization"))
	if len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") {
		raw := strings.TrimSpace(auth[7:])
		if cfg.Validator != nil {
			if raw == "" {
				return Subject{}, errUnauthorized
			}
			return cfg.Validator.Validate(ctx.Context(), raw)
		}
		// No JWT validator (typical: casbin + dev_header_user): ignore leftover Bearer.
	}
	if cfg.DevHeaderUser {
		h := http.Header{}
		if u := strings.TrimSpace(ctx.Header("X-User")); u != "" {
			h.Set("X-User", u)
		}
		s := subjectFn(ctx.Context(), h)
		if s.ID != "" {
			return s, nil
		}
	}
	return Subject{}, errUnauthorized
}

// maybeAutoBindBootstrap binds subject → bootstrapRole when the subject has no roles yet.
func maybeAutoBindBootstrap(ctx context.Context, enf *Enforcer, subject, role string) {
	subject = strings.TrimSpace(subject)
	role = strings.TrimSpace(role)
	if subject == "" || role == "" || enf == nil || !enf.Enabled() {
		return
	}
	roles, err := enf.ListSubjectRoles(ctx, subject)
	if err != nil {
		slog.Warn("authz auto-bind list roles", "subject", subject, "error", err)
		return
	}
	if len(roles) > 0 {
		return
	}
	if err := enf.AddSubjectRole(ctx, AddSubjectRoleInput{Subject: subject, Role: role}); err != nil {
		slog.Warn("authz auto-bind bootstrap role", "subject", subject, "role", role, "error", err)
		return
	}
	slog.Info("authz auto-bound bootstrap role", "subject", subject, "role", role)
}

type unauthorizedError struct{}

func (unauthorizedError) Error() string { return "missing or invalid credentials" }

var errUnauthorized unauthorizedError
