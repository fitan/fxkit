package payments

import (
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/ledger"
	"github.com/fitan/fxkit/outbox"
	"github.com/fitan/fxkit/server"

	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
)

// Module 装配 payments feature：
//   - PaymentRepo 来自共享 repository（对账/报表可复用）；
//   - ledger / outbox 都是框架模块提供的能力；
//   - webhook 用 server.RoutesLazy 挂到 chi（不经过 Huma）。
var Module = di.Options(
	fxkit.Service("payments",
		fxkit.Provide(func(i di.Injector) (*Service, error) {
			return NewService(
				di.MustInvoke[*gormx.Client](i),
				di.MustInvoke[*repository.PaymentRepo](i),
				di.MustInvoke[*ledger.Ledger](i),
				di.MustInvoke[*outbox.Store](i),
				di.MustInvoke[*outbox.Inbox](i),
				di.MustInvoke[*outbox.Config](i),
				NewMockProvider(),
				di.MustInvoke[*Config](i),
				di.MustInvoke[*di.Lifecycle](i),
			)
		}),
		fxkit.ProvideConfig[Config]("payments"),
		fxkit.RoutesLazy(func(i di.Injector) ([]server.Route, error) {
			return webhookRoutes(di.MustInvoke[*Service](i)), nil
		}),
	),
	fxhuma.ProvideRegistrarLazy(func(i di.Injector) (fxhuma.Registrar, error) {
		return Register(di.MustInvoke[*Service](i)), nil
	}),
	authz.ProvideHTTPRoutes(paymentHTTPRoutes()...),
)
