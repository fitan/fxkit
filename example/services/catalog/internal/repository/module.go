package repository

import (
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
)

// Module 汇总进程内的所有仓储实现。
//
// 约束：本包只能向下依赖 entity，绝不能 import article / comment / system，
// 否则会与 feature -> repository 的依赖形成 import cycle。
var Module = di.Options(
	fxkit.Service("repository",
		fxkit.Provide(func(i di.Injector) (*ArticleRepo, error) {
			return NewArticleRepo(di.MustInvoke[*gormx.Client](i)), nil
		}),
		fxkit.Provide(func(i di.Injector) (*CommentRepo, error) {
			return NewCommentRepo(di.MustInvoke[*gormx.Client](i)), nil
		}),
		fxkit.Provide(func(i di.Injector) (*PaymentRepo, error) {
			return NewPaymentRepo(di.MustInvoke[*gormx.Client](i)), nil
		}),
		fxkit.Provide(func(i di.Injector) (*CounterRepo, error) {
			return NewCounterRepo(di.MustInvoke[*gormx.Client](i)), nil
		}),
		fxkit.Provide(func(i di.Injector) (*HeartbeatRepo, error) {
			return NewHeartbeatRepo(di.MustInvoke[*gormx.Client](i)), nil
		}),
	),
)
