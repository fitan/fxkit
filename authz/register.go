package authz

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fitan/fxkit/config"
	"go.uber.org/fx"
)

type autoRegisterParams struct {
	fx.In
	LC       fx.Lifecycle
	Config   *config.Config
	Enforcer *Enforcer
	Routes   []HTTPRoute `group:"authz_http_routes"`
}

// registerRoutesOnStart syncs ProvideHTTPRoutes → Casbin policies for bootstrap_role.
func registerRoutesOnStart(p autoRegisterParams) {
	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if p.Enforcer == nil || !p.Enforcer.Enabled() || p.Config == nil {
				return nil
			}
			ac := p.Config.Get().Auth.Casbin
			if !ac.AutoRegisterRoutes {
				return nil
			}
			role := strings.TrimSpace(ac.BootstrapRole)
			if role == "" {
				role = "admin"
			}
			c, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()

			policies := PoliciesFromRoutes(role, p.Routes)
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
			if err := p.Enforcer.SyncRouteCatalog(p.Routes); err != nil {
				return fmt.Errorf("authz sync route catalog: %w", err)
			}
			var bindings []RoleBinding
			for _, u := range ac.BootstrapUsers {
				u = strings.TrimSpace(u)
				if u == "" {
					continue
				}
				bindings = append(bindings, RoleBinding{User: u, Role: role})
			}
			if len(bindings) > 0 {
				if err := p.Enforcer.AddRoleBindings(bindings); err != nil {
					return fmt.Errorf("authz auto-register bindings: %w", err)
				}
			}
			slog.InfoContext(c, "authz auto-registered routes",
				"role", role,
				"policies", len(policies),
				"catalog", len(p.Routes),
				"users", len(bindings),
			)
			return nil
		},
	})
}

// PoliciesFromRoutes builds Casbin p rules: role may Method on Path.
// Empty Method becomes "*".
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
		out = append(out, Policy{Sub: role, Obj: path, Act: act})
	}
	return out
}
