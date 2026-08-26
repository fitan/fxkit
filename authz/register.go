package authz

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"go.uber.org/fx"
)

type autoRegisterParams struct {
	fx.In
	LC       fx.Lifecycle
	Config   *Config
	Enforcer *Enforcer
	Routes   []HTTPRoute `group:"authz_http_routes"`
	API      huma.API    `optional:"true"`
}

// registerRoutesOnStart syncs ProvideHTTPRoutes plus live Huma OpenAPI operations
// into Casbin policies for bootstrap_role (so RegisterResource is covered).
func registerRoutesOnStart(p autoRegisterParams) {
	p.LC.Append(fx.Hook{
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
			if err := p.Enforcer.SyncRouteCatalog(routes); err != nil {
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
				"catalog", len(routes),
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
