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
	"github.com/fitan/fxkit/config"
	fxhuma "github.com/fitan/fxkit/huma"
	"go.uber.org/fx"
)

// Module 注册 JWT 校验器、Casbin enforcer、启动时路由权限自动登记、
// RBAC 管理 API（/authz/*）与 Huma 鉴权中间件。
var Module = fx.Module("fxkit/authz",
	config.Provide[Config]("auth"),
	fx.Provide(NewValidator),
	fx.Provide(NewEnforcer),
	fx.Provide(fx.Annotate(defaultHTTPRoutes, fx.ResultTags(`group:"authz_http_routes,flatten"`))),
	fx.Provide(fx.Annotate(adminHTTPRoutes, fx.ResultTags(`group:"authz_http_routes,flatten"`))),
	fxhuma.ProvideMiddleware(provideHumaMiddleware),
	fxhuma.ProvideRegistrar(provideAdminRegistrar),
	fx.Invoke(registerRoutesOnStart),
)

// validatorParams is the Fx input for [NewValidator].
type validatorParams struct {
	fx.In
	Config *Config
	LC     fx.Lifecycle `optional:"true"`
}
