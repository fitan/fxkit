// Command fxkit 是 fxkit 框架的脚手架 CLI。
//
//	fxkit new <name>                  生成新项目（默认模板）
//	fxkit new <name> --module path    设置 Go module 路径
//	fxkit new <name> --minimal        脚手架 HTTP-only 服务（无 Dapr）
//	fxkit new <name> --no-dapr        脚手架不含 Dapr 资源
//	fxkit new <name> --no-db          脚手架不含 gormx
//	fxkit new <name> --skip-tidy      跳过生成后的 `go mod tidy`
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
	root.AddCommand(newCmd())
	root.AddCommand(genCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
