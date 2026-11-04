// Command fxkit 是 fxkit 框架的脚手架 CLI。
//
//	fxkit init <name>                 生成 monorepo（单 go.mod + services/）
//	fxkit init <name> --module path   设置 Go module 路径
//	fxkit init <name> --fxkit-path p  用本地 fxkit 路径（replace），跳过版本解析
//	fxkit new <name>                  在当前仓库 services/<name> 下加一个服务
//	fxkit new <name> --minimal        使用 fxkit.Minimal() + Huma
//	fxkit new <name> --out ./internal/api  指定位置（import path 按实际路径推导）
//	fxkit new <name> --skip-tidy      跳过生成后的 `go mod tidy`
//	fxkit gen resource <Name> ...     生成 crudx + Huma resource
//	fxkit gen client --spec ... --out 从 OpenAPI 生成 reqx 客户端
//
// 生成的 go.mod 需要一个具体版本，按 FXKIT_MODULE_VERSION 环境变量 →
// -ldflags 注入（main.fxkitModuleVersionInjected）→ build info 的顺序解析。
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
		// 错误已经由下面统一打印，避免 cobra 再打一遍并把整段 help 甩到 stderr。
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(initCmd())
	root.AddCommand(newCmd())
	root.AddCommand(genCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
