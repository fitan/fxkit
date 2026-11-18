package system

import (
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/cachex"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/server"

	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
)

var Module = di.Options(
	fxkit.Service("system",
		fxkit.Provide(func(i di.Injector) (*Service, error) {
			cache, _ := di.Invoke[cachex.Cache](i)
			return NewService(
				di.MustInvoke[*gormx.Client](i),
				di.MustInvoke[*repository.CounterRepo](i),
				di.MustInvoke[*repository.HeartbeatRepo](i),
				di.MustInvoke[*hatchetx.Client](i),
				di.MustInvoke[*authz.Enforcer](i),
				di.MustInvoke[*Config](i),
				cache,
				di.MustInvoke[*di.Lifecycle](i),
			)
		}),
		fxkit.ProvideConfig[Config]("catalog"),
		fxkit.Routes(chiRoutes()...),
	),
	server.ProvideMiddleware(chiHeader()),
	fxhuma.ProvideMiddleware(humaHeader()),
	fxhuma.ProvideRegistrarLazy(func(i di.Injector) (fxhuma.Registrar, error) {
		return Register(di.MustInvoke[*Service](i)), nil
	}),
	hatchetx.ProvideRegistrarLazy(func(i di.Injector) (hatchetx.Registrar, error) {
		return RegisterWorker(di.MustInvoke[*Service](i)), nil
	}),
	authz.ProvideHTTPRoutes(systemHTTPRoutes()...),
	// 业务级就绪探针：验证 counter 表真的可读（连接级 Ping 通过但迁移未完成时能提前发现）
	ProvideReadiness(),
)
