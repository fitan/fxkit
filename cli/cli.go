// Package cli 是框架的 Cobra 入口。默认根命令支持 `serve` 子命令，从传入 [Run] 的模块构造 Fx 图。
// 宿主应用可在调用 [Run] 前通过 [AddCommand] 注册额外命令。
package cli

import (
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/logx"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

var (
	rootName        = "fxkit-app"
	rootShort       = "fxkit service"
	rootCmd         *cobra.Command
	extraCommandsMu sync.Mutex
	extraCommands   []*cobra.Command
)

// SetRootName 自定义 cobra 根命令的 Use 与 Short。在 [Run] 之前调用（例如 main 中），
// 为二进制设置独立的帮助信息。
func SetRootName(name, short string) {
	if name != "" {
		rootName = name
	}
	if short != "" {
		rootShort = short
	}
}

// AddCommand 注册额外 cobra 命令，在 Run 设置根命令时挂载。可在 init() 或 main() 中安全调用。
func AddCommand(cmds ...*cobra.Command) {
	extraCommandsMu.Lock()
	defer extraCommandsMu.Unlock()
	extraCommands = append(extraCommands, cmds...)
}

// Run 构建 cobra 根命令与 `serve` 子命令并执行。serve 子命令从用户提供的 options 组装 fx.App。
//
// 全局 deferred recover 防止意外 panic，使其在进程以 code 1 退出前写入 stderr
// （OTel 日志管道安装后也会进入该管道）。
func Run(opts ...fx.Option) {
	logx.SetupDefault()

	defer func() {
		if err := recover(); err != nil {
			slog.Error("panic recovered",
				"error", err,
				"stack", string(debug.Stack()),
			)
			os.Exit(1)
		}
	}()

	rootCmd = &cobra.Command{
		Use:   rootName,
		Short: rootShort,
	}
	rootCmd.PersistentFlags().StringP("config", "c", "configs/config.yaml", "local config file path (optional when --consul is set)")
	rootCmd.PersistentFlags().String("consul", "", "Consul HTTP address to load config from (e.g. localhost:8500)")
	rootCmd.PersistentFlags().String("consul-key", "", "Consul KV key holding YAML config; required with --consul")
	rootCmd.AddCommand(buildServeCmd(opts))
	rootCmd.AddCommand(buildVersionCmd())

	extraCommandsMu.Lock()
	for _, c := range extraCommands {
		rootCmd.AddCommand(c)
	}
	extraCommandsMu.Unlock()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func buildServeCmd(opts []fx.Option) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			configFile, _ := cmd.Flags().GetString("config")
			consulAddr, _ := cmd.Flags().GetString("consul")
			consulKey, _ := cmd.Flags().GetString("consul-key")
			port, _ := cmd.Flags().GetString("port")

			app := fx.New(append([]fx.Option{
				fx.StartTimeout(60 * time.Second),
				fx.WithLogger(func() fxevent.Logger { return logx.NewFxLogger(os.Stderr) }),
				fx.Supply(config.Options{
					ConfigFile:      configFile,
					ConsulAddress:   consulAddr,
					ConsulConfigKey: consulKey,
					Port:            port,
				}),
			}, opts...)...)
			app.Run()
			return nil
		},
	}
	cmd.Flags().StringP("port", "p", "", "server port (overrides config)")
	return cmd
}

func buildVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build info",
		Run: func(cmd *cobra.Command, _ []string) {
			info := buildinfo.Get()
			fmt.Printf("version:    %s\ncommit:     %s\ngit_remote: %s\ngit_branch: %s\ndirty:      %v\ngo_version: %s\nbuilt:      %s\nby:         %s\n",
				info.Version, info.Commit, info.GitRemote, info.GitBranch,
				info.Dirty, info.GoVersion, info.BuildTime, info.BuiltBy,
			)
		},
	}
}

// Module 为与其他 fxkit 包对称而设的占位符。实际装配在 [Run] 内完成，由 [Run] 自行构造 fx.App。
var Module = fx.Module("fxkit/cli")
