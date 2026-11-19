package authz

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/di"
)

// autoRegisterParams 是启动时自动登记 Casbin 策略的入参。
type autoRegisterParams struct {
	// LC 是生命周期，用于挂载启动 hook。
	LC *di.Lifecycle
	// Config 是 auth 配置，读取其中的 casbin 段。
	Config *Config
	// Enforcer 是 Casbin 执行器；未启用时自动登记被跳过。
	Enforcer *Enforcer
	// Routes 是已通过 ProvideHTTPRoutes 登记的路由。
	Routes []HTTPRoute
	// API 是当前 Huma API，用于补齐未登记但已发布的 operation。
	API huma.API
}

// registerRoutesOnStart 把 ProvideHTTPRoutes 与当前生效的 Huma OpenAPI operation
// 同步为 bootstrap_role 的 Casbin 策略（从而覆盖 RegisterResource）。
func registerRoutesOnStart(p autoRegisterParams) {
	if p.LC != nil {
		p.LC.Append(di.Hook{
			OnStart: func(ctx context.Context) error {
				if p.Enforcer == nil || !p.Enforcer.Enabled() || p.Config == nil {
					return nil
				}
				ac := p.Config.Casbin
				if !ac.AutoRegisterRoutes {
					return nil
				}
				role := strings.TrimSpace(ac.BootstrapRole)
				c, cancel := context.WithTimeout(ctx, 8*time.Second)
				defer cancel()

				routes := mergeHTTPRoutes(p.Routes, HTTPRoutesFromOpenAPI(p.API))
				policies := PoliciesFromRoutes(role, routes)
				if len(policies) > 0 {
					if err := p.Enforcer.AddPolicies(policies); err != nil {
						return fmt.Errorf("authz auto-register policies: %w", err)
					}
				}
				if _, err := p.Enforcer.EnsureRole(c, CreateRoleInput{
					Name:        role,
					DisplayName: role,
					Description: "bootstrap role (auto-register)",
				}); err != nil {
					return fmt.Errorf("authz ensure bootstrap role: %w", err)
				}
				// 目录是运行时从路由表 + 策略派生的，不再需要启动时写库同步。
				var bindings []RoleBinding
				for _, u := range ac.BootstrapUsers {
					u = strings.TrimSpace(u)
					if u == "" {
						continue
					}
					bindings = append(bindings, RoleBinding{User: u, Role: role, Dom: DefaultDomain})
				}
				if len(bindings) > 0 {
					if err := p.Enforcer.AddRoleBindings(bindings); err != nil {
						return fmt.Errorf("authz auto-register bindings: %w", err)
					}
				}
				slog.InfoContext(c, "authz auto-registered routes",
					"role", role,
					"policies", len(policies),
					"catalog", len(routes),
					"users", len(bindings),
				)
				return nil
			},
		})
	}
}

// PoliciesFromRoutes 构建 Casbin p 规则：角色可以对 Path 执行 Method。
// Method 为空时变为 "*"。
func PoliciesFromRoutes(role string, routes []HTTPRoute) []Policy {
	role = strings.TrimSpace(role)
	if role == "" || len(routes) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]Policy, 0, len(routes))
	for _, r := range routes {
		path := strings.TrimSpace(r.Path)
		if path == "" {
			continue
		}
		act := strings.ToUpper(strings.TrimSpace(r.Method))
		if act == "" {
			act = "*"
		}
		key := path + "\x00" + act
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, Policy{Sub: role, Dom: DefaultDomain, Obj: path, Act: act})
	}
	return out
}
