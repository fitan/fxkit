// Package authz 为 Huma/REST 提供鉴权：JWT（或开发头 X-User）识别身份，
// Casbin 按 path + HTTP method 做 RBAC（策略存业务库 casbin_rule）。
//
// 当 auth.casbin.enabled 时，权限完全由 Casbin 决定（JWT scopes 不参与授权）。
// Casbin 关闭时，回退到 HTTPRoute.Permission + Subject.Has（兼容旧行为）。
//
// Subject.OrgID 只来自 JWT claim（auth.org_claim）；Enforce 不按组织隔离，
// 多租户须在 handler 内自行校验。多副本通过 auth.casbin.reload_interval
// 定期 LoadPolicy，避免只在本进程改策略。
//
// 仅覆盖 Huma/REST；裸 chi handler 需宿主自行鉴权。
//
// 配置见 [Config]（yaml: auth）。默认关闭。
package authz

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	fxhuma "github.com/fitan/fxkit/huma"
)

var Module = di.Module("fxkit/authz",
	config.Provide[Config]("auth"),
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (TokenValidator, error) {
			cfg, _ := di.Invoke[*Config](i)
			lc, _ := di.Invoke[*di.Lifecycle](i)
			return NewValidator(validatorParams{Config: cfg, LC: lc})
		})
		di.Provide(i, func(i di.Injector) (*Enforcer, error) {
			cfg, _ := di.Invoke[*Config](i)
			db, _ := di.Invoke[*gormx.Client](i)
			lc, _ := di.Invoke[*di.Lifecycle](i)
			return NewEnforcer(enforcerParams{Config: cfg, DB: db, LC: lc})
		})

		ProvideHTTPRoutes(adminHTTPRoutes)(i)
		fxhuma.ProvideMiddleware(provideHumaMiddleware)(i)
		fxhuma.ProvideRegistrar(provideAdminRegistrar)(i)

		lc, _ := di.Invoke[*di.Lifecycle](i)
		cfg, _ := di.Invoke[*Config](i)
		enf, _ := di.Invoke[*Enforcer](i)
		routes := di.GetGroup[HTTPRoute](i)
		api, _ := di.Invoke[huma.API](i)
		registerRoutesOnStart(autoRegisterParams{
			LC:       lc,
			Config:   cfg,
			Enforcer: enf,
			Routes:   routes,
			API:      api,
		})
	},
)

// validatorParams is the Fx input for [NewValidator].
type validatorParams struct {
	Config *Config
	LC     *di.Lifecycle `optional:"true"`
}
