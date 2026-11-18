package digest

import (
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/di"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/reqx"
)

// Module 装配 digest feature：要求 reqx.Factory（服务发现）与 storefront 段配置。
var Module = di.Options(
	fxkit.Service("digest",
		fxkit.Provide(func(i di.Injector) (*Service, error) {
			return NewService(
				di.MustInvoke[*reqx.Factory](i),
				di.MustInvoke[*Config](i),
			)
		}),
		fxkit.ProvideConfig[Config]("storefront"),
	),
	fxhuma.ProvideRegistrarLazy(func(i di.Injector) (fxhuma.Registrar, error) {
		return Register(di.MustInvoke[*Service](i)), nil
	}),
	authz.ProvideHTTPRoutes(digestHTTPRoutes()...),
)
