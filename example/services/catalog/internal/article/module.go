package article

import (
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/outbox"

	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
)

// Module 装配 article feature：
//   - Service 只依赖 gormx / repository / outbox，不依赖 HTTP；
//   - Huma 与 Hatchet 的 Registrar 都延迟到 PreStart 构造；
//   - authz 路由目录与 Huma operation 保持同一份定义。
var Module = di.Options(
	fxkit.Service("article",
		fxkit.Provide(func(i di.Injector) (*Service, error) {
			return NewService(
				di.MustInvoke[*gormx.Client](i),
				di.MustInvoke[*repository.ArticleRepo](i),
				di.MustInvoke[*outbox.Store](i),
				di.MustInvoke[*outbox.Inbox](i),
				di.MustInvoke[*outbox.Config](i),
				di.MustInvoke[*di.Lifecycle](i),
			)
		}),
	),
	fxhuma.ProvideRegistrarLazy(func(i di.Injector) (fxhuma.Registrar, error) {
		return NewHumaRegistrar(di.MustInvoke[*Service](i)), nil
	}),
	hatchetx.ProvideRegistrarLazy(func(i di.Injector) (hatchetx.Registrar, error) {
		return RegisterWorker(di.MustInvoke[*Service](i)), nil
	}),
	authz.ProvideHTTPRoutes(articleHTTPRoutes()...),
)
