// Package authz 为 Huma/REST 提供鉴权：JWT（或开发头 X-User/X-Org）识别身份，
// Casbin 按 (sub, dom, path, METHOD) 做多租户 RBAC（策略存业务库 casbin_rule）。
//
// 鉴权挂在 chi 上，覆盖 Huma、裸 chi、docs 和 MCP。Enforce 要求非空 domain：缺失 org 的请求一律拒绝；
// 跨租户管理员用 domain="*" 的 g/p 规则。
//
// 配置见 [Config]（yaml: auth）。默认关闭。
package authz

import (
	"context"
	"fmt"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/server"
)

// provideAdminRegistrarLazy 返回一个延迟构造的 admin registrar。
//
// 路由表在 registrar 真正执行（PhaseWireGroup）时才读取，
// 因此能拿到全部模块贡献完之后的完整集合。
func provideAdminRegistrarLazy(i di.Injector) (fxhuma.Registrar, error) {
	e, err := di.Invoke[*Enforcer](i)
	if err != nil {
		return nil, err
	}
	return provideAdminRegistrar(e, func() []HTTPRoute {
		return di.GetGroup[HTTPRoute](i)
	}), nil
}

// mustResolve 解析启动期必需的依赖，失败即 panic。
//
// 鉴权是安全边界：配置解析失败时返回 nil 中间件会让 huma 模块静默跳过安装
// （它对 nil 中间件直接不注册），整站因此裸奔，且只留一条日志。这里把
// 「接线错误」变成启动失败。
func mustResolve[T any](i di.Injector, what string) T {
	v, err := di.Invoke[T](i)
	if err != nil {
		panic(fmt.Sprintf("authz: resolve %s: %v", what, err))
	}
	return v
}

// Module 提供 JWT 校验、Casbin 授权、路由权限登记与管理 API。
var Module = di.Module("fxkit/authz",
	config.Provide[Config]("auth"),
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (TokenValidator, error) {
			cfg, err := di.Invoke[*Config](i)
			if err != nil {
				return nil, fmt.Errorf("authz: resolve config: %w", err)
			}
			lc := di.MustInvoke[*di.Lifecycle](i)
			return NewValidator(validatorParams{Config: cfg, LC: lc})
		})
		di.Provide(i, func(i di.Injector) (*Enforcer, error) {
			cfg, err := di.Invoke[*Config](i)
			if err != nil {
				return nil, fmt.Errorf("authz: resolve config: %w", err)
			}
			// 数据库是可选依赖（Casbin 关闭时不需要）；只有「服务不存在」可忽略，
			// 构造失败（例如 DSN 写错）必须向上报。
			db, err := di.Invoke[*gormx.Client](i)
			if err != nil {
				if !di.IsNotFound(err) {
					return nil, fmt.Errorf("authz: resolve database: %w", err)
				}
				db = nil
			}
			lc := di.MustInvoke[*di.Lifecycle](i)
			return NewEnforcer(enforcerParams{Config: cfg, DB: db, LC: lc})
		})

		ProvideHTTPRoutes(adminHTTPRoutes()...)(i)

		cfgPtr := mustResolve[*Config](i, "*Config")

		// 登记 schema 契约：仅当 Casbin 启用时才要求这些表存在。
		if cfgPtr.Casbin.Enabled {
			table := strings.TrimSpace(cfgPtr.Casbin.TableName)
			if table == "" {
				table = "casbin_rule"
			}
			gormx.RegisterSchemaContract(table, "id", "ptype", "v0", "v1", "v2", "v3", "v4", "v5")
			gormx.RegisterSchemaContract("authz_role",
				"name", "display_name", "description", "created_at", "updated_at")
		}

		server.ProvideMiddlewareLazy(provideMuxMiddleware)(i)
		fxhuma.ProvideRegistrarLazy(provideAdminRegistrarLazy)(i)

		// 自动注册 Casbin 策略依赖「完整的 Huma operation 集合」，必须等
		// huma registrar 全部安装完（同为 PhaseWireGroup，按注册顺序在 huma 之后）。
		if lc, err := di.Invoke[*di.Lifecycle](i); err == nil && lc != nil {
			lc.AppendPreStartPhase(di.PhaseAfterRegistrars, di.Hook{
				OnStart: func(context.Context) error {
					cfg, _ := di.Invoke[*Config](i)
					enf, _ := di.Invoke[*Enforcer](i)
					api, _ := di.Invoke[huma.API](i)
					registerRoutesOnStart(autoRegisterParams{
						LC:       lc,
						Config:   cfg,
						Enforcer: enf,
						Routes:   di.GetGroup[HTTPRoute](i),
						API:      api,
					})
					return nil
				},
			})
		}
	},
)

// validatorParams 是 [NewValidator] 的 Fx 入参。
type validatorParams struct {
	Config *Config
	LC     *di.Lifecycle `optional:"true"`
}
