// Command fxkit 是 fxkit 框架的脚手架 CLI。
//
//	fxkit init <name>                 生成 monorepo（单 go.mod + services/）
//	fxkit init <name> --module path   设置 Go module 路径
//	fxkit new <name>                  在当前仓库 services/<name> 下加一个服务
//	fxkit new <name> --minimal        使用 fxkit.Minimal() + Huma
//	fxkit new <name> --skip-tidy      跳过生成后的 `go mod tidy`
//	fxkit gen resource <Name> ...     生成 crudx + Huma resource
//	fxkit gen client --spec ... --out 从 OpenAPI 生成 reqx 客户端
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "fxkit",
		Short: "fxkit scaffolding CLI",
	}
	root.AddCommand(initCmd())
	root.AddCommand(newCmd())
	root.AddCommand(genCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
