package main

import (
	_ "embed"

	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/cli"
	fxhuma "github.com/fitan/fxkit/huma"

	"github.com/fitan/fxkit-example/internal/stack"
	"github.com/fitan/fxkit-example/services/storefront/internal/digest"
)

//go:embed openapi.base.yaml
var openAPIBase []byte

func main() {
	cli.SetRootName("storefront", "storefront service")
	fxkit.Run(
		stack.WithMergedDocs(openAPIBase),
		fxhuma.Module,
		digest.Module,
	)
}
