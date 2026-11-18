package demo

import (
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/cachex"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/outbox"
)

// Module 演示框架的可选能力。所有依赖都是可选的：
// 缓存/Consul/Hatchet 未启用时，对应接口返回 503，不影响其它接口。
var Module = di.Options(
	fxkit.Service("demo",
		fxkit.ProvideConfig[DemoConfig]("demo"),
		fxkit.Provide(func(i di.Injector) (*Service, error) {
			cache, _ := di.Invoke[cachex.Cache](i)
			dynamic, _ := di.Invoke[config.Dynamic[DemoConfig]](i)
			locker, _ := di.Invoke[consulx.Locker](i)
			return NewService(
				cache,
				dynamic,
				di.MustInvoke[*DemoConfig](i),
				locker,
				di.MustInvoke[*outbox.Store](i),
				di.MustInvoke[*outbox.Config](i),
				di.MustInvoke[*hatchetx.Client](i),
				di.MustInvoke[*gormx.Client](i),
			)
		}),
	),
	// Consul KV 热更新：只替换宿主业务配置快照，不重启框架对象。
	config.ProvideWatch[DemoConfig]("demo"),
	fxhuma.ProvideRegistrarLazy(func(i di.Injector) (fxhuma.Registrar, error) {
		return Register(di.MustInvoke[*Service](i)), nil
	}),
	hatchetx.ProvideRegistrarLazy(func(i di.Injector) (hatchetx.Registrar, error) {
		return RegisterWorker(di.MustInvoke[*Service](i)), nil
	}),
	authz.ProvideHTTPRoutes(demoHTTPRoutes()...),
)
