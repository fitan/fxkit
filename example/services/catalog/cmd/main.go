package main

import (
	_ "embed"

	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/cli"
	fxhuma "github.com/fitan/fxkit/huma"

	"github.com/fitan/fxkit-example/internal/stack"
	"github.com/fitan/fxkit-example/services/catalog/internal/article"
	"github.com/fitan/fxkit-example/services/catalog/internal/comment"
	"github.com/fitan/fxkit-example/services/catalog/internal/doctor"
	"github.com/fitan/fxkit-example/services/catalog/internal/platform"
)

//go:embed openapi.base.yaml
var openAPIBase []byte

func main() {
	cli.SetRootName("catalog", "catalog service")
	cli.AddCommand(doctor.Command())
	fxkit.Run(
		stack.WithMergedDocs(openAPIBase),
		fxhuma.Module,
		article.Module,
		comment.Module,
		platform.Module,
	)
}
