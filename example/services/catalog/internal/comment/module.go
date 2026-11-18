package comment

import (
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	fxhuma "github.com/fitan/fxkit/huma"

	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
)

// Module 装配 comment feature；comments 段配置用 fxkit.ProvideConfig 注入。
var Module = di.Options(
	fxkit.Service("comment",
		fxkit.Provide(func(i di.Injector) (*Service, error) {
			return NewService(
				di.MustInvoke[*gormx.Client](i),
				di.MustInvoke[*repository.CommentRepo](i),
				di.MustInvoke[*Config](i),
				di.MustInvoke[*di.Lifecycle](i),
			)
		}),
		fxkit.ProvideConfig[Config]("comments"),
	),
	fxhuma.ProvideRegistrarLazy(func(i di.Injector) (fxhuma.Registrar, error) {
		return Register(di.MustInvoke[*Service](i)), nil
	}),
	authz.ProvideHTTPRoutes(commentHTTPRoutes()...),
)
