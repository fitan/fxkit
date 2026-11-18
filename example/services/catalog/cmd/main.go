package main

import (
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/audit"
	fxcli "github.com/fitan/fxkit/cli"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/idempotency"
	"github.com/fitan/fxkit/ledger"

	"github.com/fitan/fxkit-example/internal/stack"
	catalogapi "github.com/fitan/fxkit-example/services/catalog/api"
	"github.com/fitan/fxkit-example/services/catalog/internal/article"
	catalogcli "github.com/fitan/fxkit-example/services/catalog/internal/cli"
	"github.com/fitan/fxkit-example/services/catalog/internal/comment"
	"github.com/fitan/fxkit-example/services/catalog/internal/demo"
	"github.com/fitan/fxkit-example/services/catalog/internal/payments"
	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
	"github.com/fitan/fxkit-example/services/catalog/internal/system"
)

// catalog 进程入口：只做装配，不含业务逻辑。
//
// 顺序：stack（框架默认能力）→ repository（共享仓储）→ 各 feature module。
// 每个 Module 自己提供 Service / Huma / Hatchet / authz 路由，main 不关心细节。
func main() {
	fxcli.SetRootName("catalog", "catalog service")
	fxcli.AddCommand(catalogcli.DoctorCommand())
	fxkit.Run(
		stack.WithMergedDocs(catalogapi.Base),
		fxhuma.Module,
		repository.Module,
		article.Module,
		comment.Module,
		system.Module,
		demo.Module,
		ledger.Module,
		payments.Module,
		audit.Module,
		idempotency.Module,
	)
}
