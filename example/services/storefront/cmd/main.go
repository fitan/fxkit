package main

import (
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/audit"
	"github.com/fitan/fxkit/cli"
	fxhuma "github.com/fitan/fxkit/huma"

	"github.com/fitan/fxkit-example/internal/stack"
	storefrontapi "github.com/fitan/fxkit-example/services/storefront/api"
	"github.com/fitan/fxkit-example/services/storefront/internal/digest"
)

// storefront 进程入口：只装配 digest feature（调用 catalog 的示例）。
func main() {
	cli.SetRootName("storefront", "storefront service")
	fxkit.Run(
		stack.WithMergedDocs(storefrontapi.Base),
		fxhuma.Module,
		digest.Module,
		audit.Module,
	)
}
