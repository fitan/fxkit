// Package cli 是框架的 Cobra 入口。默认根命令支持 `serve` / `openapi` / `version`，
// 从传入 [Run] 的模块构造依赖注入图。宿主应用可在调用 [Run] 前通过 [AddCommand] 注册额外命令。
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/logx"
	"github.com/spf13/cobra"
)

const (
	defaultStartTimeout = 60 * time.Second
	defaultStopTimeout  = 30 * time.Second
)

var (
	rootName        = "fxkit-app"
	rootShort       = "fxkit service"
	rootCmd         *cobra.Command
	extraCommandsMu sync.Mutex
	extraCommands   []*cobra.Command
)

// SetRootName 自定义 cobra 根命令的 Use 与 Short。
func SetRootName(name, short string) {
	if name != "" {
		rootName = name
	}
	if short != "" {
		rootShort = short
	}
}

// AddCommand 注册额外 cobra 命令。
func AddCommand(cmds ...*cobra.Command) {
	extraCommandsMu.Lock()
	defer extraCommandsMu.Unlock()
	extraCommands = append(extraCommands, cmds...)
}

// Run 构建 cobra 根命令与 `serve` / `openapi` / `version` 子命令并执行。
func Run(opts ...di.Option) {
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
	rootCmd.AddCommand(buildOpenAPICmd(opts))
	rootCmd.AddCommand(buildMCPCmd(opts))
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

func buildServeCmd(opts []di.Option) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			startTO, stopTO := timeoutsFromCmd(cmd)
			root := newApp(cmd, opts)

			lc, err := di.Invoke[*di.Lifecycle](root)
			if err != nil {
				return err
			}

			startCtx, cancel := context.WithTimeout(context.Background(), startTO)
			defer cancel()
			if err := lc.Start(startCtx); err != nil {
				return err
			}

			// 等待退出信号
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
			<-quit

			stopCtx, stopCancel := context.WithTimeout(context.Background(), stopTO)
			defer stopCancel()
			if err := lc.Stop(stopCtx); err != nil {
				return err
			}
			_ = root.Shutdown()
			return nil
		},
	}
	cmd.Flags().StringP("port", "p", "", "server port (overrides config)")
	cmd.Flags().Duration("start-timeout", defaultStartTimeout, "OnStart timeout")
	cmd.Flags().Duration("stop-timeout", defaultStopTimeout, "OnStop / HTTP Shutdown timeout")
	return cmd
}

func timeoutsFromCmd(cmd *cobra.Command) (start, stop time.Duration) {
	start, stop = defaultStartTimeout, defaultStopTimeout
	if cmd == nil {
		return
	}
	if v, err := cmd.Flags().GetDuration("start-timeout"); err == nil && v > 0 {
		start = v
	}
	if v, err := cmd.Flags().GetDuration("stop-timeout"); err == nil && v > 0 {
		stop = v
	}
	return
}

func newApp(cmd *cobra.Command, opts []di.Option, extra ...di.Option) *di.RootScope {
	configFile, _ := cmd.Flags().GetString("config")
	consulAddr, _ := cmd.Flags().GetString("consul")
	consulKey, _ := cmd.Flags().GetString("consul-key")
	port, _ := cmd.Flags().GetString("port")

	supplyOpt := func(i di.Injector) {
		di.ProvideValue(i, config.Options{
			ConfigFile:      configFile,
			ConsulAddress:   consulAddr,
			ConsulConfigKey: consulKey,
			Port:            port,
		})
	}

	all := []di.Option{supplyOpt}
	all = append(all, opts...)
	all = append(all, extra...)
	return di.New(all...)
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

// Module 模块占位符。
var Module = di.Module("fxkit/cli")
