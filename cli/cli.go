// Package cli 是框架的 Cobra 入口。默认根命令支持 `serve` / `openapi` / `version`，
// 从传入 [Run] 的模块构造依赖注入图。宿主应用可在调用 [Run] 前通过 [AddCommand] 注册额外命令。
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/logx"
	"github.com/spf13/cobra"
)

const (
	// defaultStartTimeout 是 serve 启动（OnStart）的默认超时。
	defaultStartTimeout = 60 * time.Second
	// defaultStopTimeout 是 serve 停机（OnStop / HTTP Shutdown）的默认超时。
	defaultStopTimeout = 30 * time.Second
)

var (
	// rootName 是 cobra 根命令 Use 的默认值，可由 [SetRootName] 覆盖。
	rootName = "fxkit-app"
	// rootShort 是 cobra 根命令 Short 的默认值，可由 [SetRootName] 覆盖。
	rootShort = "fxkit service"
	// rootCmd 是 [Run] 构建的根命令，仅在 Run 内赋值。
	rootCmd *cobra.Command
	// extraCommands 是 [AddCommand] 登记、由 [Run] 挂到根命令下的额外命令。
	extraCommands []*cobra.Command
)

// SetRootName 自定义 cobra 根命令的 Use 与 Short。
//
// 按文档必须在 Run 之前调用（单线程装配期），因此不需要加锁：
// 早期实现的 sync.Mutex 保护的是一个不存在并发的时间窗口。
func SetRootName(name, short string) {
	if name != "" {
		rootName = name
	}
	if short != "" {
		rootShort = short
	}
}

// AddCommand 注册额外 cobra 命令（同样必须在 Run 之前调用）。
func AddCommand(cmds ...*cobra.Command) {
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
	rootCmd.AddCommand(buildValidateCmd(opts))
	rootCmd.AddCommand(buildMigrateCmd(opts))
	rootCmd.AddCommand(buildVersionCmd())

	for _, c := range extraCommands {
		rootCmd.AddCommand(c)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// buildServeCmd 构建 serve 子命令：装配对象图并启动生命周期，收到 SIGINT/SIGTERM 后停机。
// --di-graph 只构建并打印对象图后退出，不监听端口。
func buildServeCmd(opts []di.Option) *cobra.Command {
	var (
		diGraph    bool
		readyWait  time.Duration
		warmupMode string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch warmupMode {
			case warmupOff, warmupBeforeStart, warmupReport:
			default:
				// 拼错时静默无效比报错难排错得多。
				return fmt.Errorf("invalid --warmup %q (want %s|%s|%s)",
					warmupMode, warmupOff, warmupBeforeStart, warmupReport)
			}

			startTO, stopTO := timeoutsFromCmd(cmd)
			root := newApp(cmd, opts)
			// 无论启动成功、失败还是信号退出，都要关闭容器（触发 provider Shutdown）。
			// 早期只在正常路径调用，启动失败时数据库/HTTP 客户端等资源会泄漏。
			defer root.Shutdown()

			lc, err := di.Invoke[*di.Lifecycle](root)
			if err != nil {
				return err
			}

			if diGraph {
				// 只构建对象图、不启动服务：把容器状态打到 stdout，便于 CI 断言与排查。
				// 这里先跑 PreStart（解析延迟构造函数、固化 group），再输出报告。
				if err := lc.StartPreStartOnly(cmd.Context()); err != nil {
					return err
				}
				fmt.Fprint(cmd.OutOrStdout(), di.Report(root))
				if warmupMode != warmupOff {
					printWarmup(cmd.OutOrStdout(), di.Warmup(root))
				}
				return nil
			}

			// warmup=before-start：在监听端口前强制解析所有服务。
			// 这让 lazy 注册的服务（构造函数返回 error / 依赖缺失）在启动期暴露，
			// 而不是等某个低频请求路径第一次用到它。
			//
			// 注意：不能在这里单独调 StartPreStartOnly —— 那会把 huma registrar
			// 跑一遍，随后 lc.Start 又跑一遍，导致 operation ID 重复注册而 panic。
			// 正确做法是注册成 PhaseMountHTTP+1 的 PreStart 钩子（见 registerWarmup）。
			// 它不是最后一档：PhaseAfterHTTPServer（210）在它之后。
			if warmupMode == warmupBeforeStart {
				registerWarmup(root)
			}

			startCtx, cancel := context.WithTimeout(context.Background(), startTO)
			defer cancel()
			startAt := time.Now()
			startErr := lc.Start(startCtx)
			recordStartup("start", time.Since(startAt), startErr)
			if startErr != nil {
				return startErr
			}

			if readyWait > 0 {
				// 启动后自检：给后台组件一点时间就绪，再打印一次容器状态。
				go func() {
					time.Sleep(readyWait)
					slog.Info("di graph snapshot", "report", di.Report(root))
				}()
			}

			// 等待退出信号
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
			defer signal.Stop(quit)
			<-quit

			stopCtx, stopCancel := context.WithTimeout(context.Background(), stopTO)
			defer stopCancel()
			stopAt := time.Now()
			stopErr := lc.Stop(stopCtx)
			recordStartup("stop", time.Since(stopAt), stopErr)
			if stopErr != nil {
				return stopErr
			}
			return nil
		},
	}
	cmd.Flags().StringP("port", "p", "", "server port (overrides config)")
	cmd.Flags().Duration("start-timeout", defaultStartTimeout, "OnStart timeout")
	cmd.Flags().Duration("stop-timeout", defaultStopTimeout, "OnStop / HTTP Shutdown timeout")
	cmd.Flags().BoolVar(&diGraph, "di-graph", false, "print the DI graph (services + groups) after wiring and exit")
	cmd.Flags().DurationVar(&readyWait, "di-report-after", 0, "log a DI snapshot this long after startup (e.g. 2s)")
	cmd.Flags().StringVar(&warmupMode, "warmup", warmupOff,
		"pre-resolve all DI services: off (default) | before-start | report\n"+
			"  before-start: construct every service before listening; abort on error\n"+
			"  report: only print the result (with --di-graph)")
	return cmd
}

// warmup 模式的取值。命名成常量而不是裸字符串，避免 CLI flag 拼错时静默无效。
const (
	// warmupOff 关闭预热，是 --warmup 的默认值。
	warmupOff = "off"
	// warmupBeforeStart 在监听端口前构造全部服务，任一失败即中止启动。
	warmupBeforeStart = "before-start"
	// warmupReport 只打印预热结果，需配合 --di-graph 使用。
	warmupReport = "report"
)

// printWarmup 打印预热结果（不中止流程）。
func printWarmup(w io.Writer, report di.WarmupReport) {
	fmt.Fprintf(w, "warmup: resolved=%d failed=%d skipped=%d\n",
		len(report.Resolved), len(report.Failed), len(report.Optional))
	for _, f := range report.Failed {
		fmt.Fprintf(w, "  FAILED   %s: %v\n", f.Service, f.Err)
	}
	for _, o := range report.Optional {
		fmt.Fprintf(w, "  SKIPPED  %s: %v\n", o.Service, o.Err)
	}
}

// registerWarmup 把「预热全部服务」注册为 PhaseMountHTTP+1（201）。
//
// 此时延迟构造函数已求值、group 已完整、huma operation 已注册，服务尚未对外监听。
// 它不是 PreStart 的最后一档：PhaseAfterHTTPServer（210）在它之后，
// 那里才第一次解析的服务不在这次预热里。
// 任何已解析服务构造失败都会中止启动。
func registerWarmup(root di.Injector) {
	lc, err := di.Invoke[*di.Lifecycle](root)
	if err != nil || lc == nil {
		return
	}
	lc.AppendPreStartPhase(di.PhaseMountHTTP+1, di.Hook{
		OnStart: func(context.Context) error {
			return warmupOrFail(root)
		},
	})
}

// warmupOrFail 预热并在有失败项时中止启动。
func warmupOrFail(root di.Injector) error {
	report := di.Warmup(root)
	if err := report.Error(); err != nil {
		return fmt.Errorf("warmup: %w (use --warmup=off to skip pre-resolution)", err)
	}
	slog.Info("di warmup complete",
		"resolved", len(report.Resolved),
		"skipped", len(report.Optional),
	)
	return nil
}

// timeoutsFromCmd 读取 --start-timeout / --stop-timeout；缺失、非法或非正值时回退到默认值。
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

// newApp 把命令行 flag 装成 [config.Options]，再叠加调用方 opts 与 extra，构造 DI 根作用域。
// 返回的根作用域由调用方负责 Shutdown。
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

// buildVersionCmd 构建 version 子命令，把 [buildinfo.Get] 的结果打印到 stdout。
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
