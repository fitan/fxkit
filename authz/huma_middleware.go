package authz

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/di"
	fxhuma "github.com/fitan/fxkit/huma"
)

// provideHumaMiddleware 返回一个延迟构建的中间件注册器。
//
// 关键：HTTPRoute 必须在 [di.PhaseWireGroup]（所有模块装配完成、所有构造函数形式的
// 贡献点已求值）时读取，而不是在 PhaseResolveContributors 里预先把 routes 固化进
// params 结构体——否则业务模块用 func(*Svc) []HTTPRoute 登记的路由会被漏掉，
// Casbin 关闭时这些 operation 的 Permission 校验会被静默跳过。
func provideHumaMiddleware(inj di.Injector) fxhuma.MiddlewareRegistrar {
	return fxhuma.MiddlewareFunc(func(api huma.API) fxhuma.Middleware {
		cfgPtr, err := di.Invoke[*Config](inj)
		if err != nil {
			slog.Error("authz: resolve config failed", "error", err)
			return nil
		}
		validator, _ := di.Invoke[TokenValidator](inj)
		enforcer, _ := di.Invoke[*Enforcer](inj)

		cfg := Config{}
		if cfgPtr != nil {
			cfg = *cfgPtr
		}
		if cfg.Enabled && cfg.DevHeaderUser {
			slog.Warn("auth.dev_header_user enabled: X-User bypasses JWT — disable in production")
		}
		if cfg.Enabled && !cfg.DenyUnregistered && (enforcer == nil || !enforcer.Enabled()) {
			slog.Warn("auth.deny_unregistered=false: Huma ops still require login; unlisted ops are login-only (not anonymous)")
		}
		if cfg.Casbin.AutoBindBootstrap {
			slog.Warn("auth.casbin.auto_bind_bootstrap enabled: unbound subjects get bootstrap role — disable in production")
		}

		return NewHumaMiddleware(HumaMiddlewareConfig{
			Enabled:           cfg.Enabled,
			DevHeaderUser:     cfg.DevHeaderUser,
			DenyUnregistered:  cfg.DenyUnregistered,
			AutoBindBootstrap: cfg.Casbin.AutoBindBootstrap,
			BootstrapRole:     strings.TrimSpace(cfg.Casbin.BootstrapRole),
			Validator:         validator,
			Enforcer:          enforcer,
			Routes:            di.GetGroup[HTTPRoute](inj),
			SubjectFunc:       DefaultDevSubjectFunc,
		}).Middleware(api)
	})
}

// HumaMiddlewareConfig 配置 [NewHumaMiddleware]。
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

	// auto-bind 只需要每个 subject 检查一次；早期实现每个请求都查一次角色表，
	// 在高 QPS 下形成额外的 DB 压力。
	var (
		bindMu   sync.Mutex
		bindSeen = map[string]struct{}{}
	)
	const maxBindCache = 4096
	ensureBootstrap := func(ctx context.Context, subject string) {
		bindMu.Lock()
		_, seen := bindSeen[subject]
		if seen {
			bindMu.Unlock()
			return
		}
		if len(bindSeen) >= maxBindCache {
			bindSeen = map[string]struct{}{}
		}
		bindMu.Unlock()

		maybeAutoBindBootstrap(ctx, enf, subject, cfg.BootstrapRole)

		bindMu.Lock()
		bindSeen[subject] = struct{}{}
		bindMu.Unlock()
	}

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
					ensureBootstrap(ctx.Context(), subj.ID)
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

// OpPublic 是 huma.Operation.Metadata 中用于跳过该 operation 鉴权的键。
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
		// 没有 JWT 校验器时（典型场景：casbin + dev_header_user）：忽略残留的 Bearer。
	}
	if cfg.DevHeaderUser {
		// 先读 header，避免无 X-User 的请求每次都分配一个 http.Header。
		if u := strings.TrimSpace(ctx.Header("X-User")); u != "" {
			h := http.Header{}
			h.Set("X-User", u)
			if s := subjectFn(ctx.Context(), h); s.ID != "" {
				return s, nil
			}
		}
	}
	return Subject{}, errUnauthorized
}

// maybeAutoBindBootstrap 在主体尚无角色时绑定 主体 → bootstrapRole。
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
