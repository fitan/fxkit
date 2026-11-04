package cli

import (
	"fmt"
	"log/slog"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/spf13/cobra"
)

// buildValidateCmd 提供 `validate`：加载配置、跑一遍所有模块的 Load/Validate，
// 并报告「配置里写了但没有模块读取」的死键。
//
// 设计约束：validate 必须能在**没有任何基础设施**的环境下运行（CI、本地无 DB），
// 因此它只构建配置层、不执行 PreStart/OnStart —— 那些阶段会连数据库、
// 启动 worker，属于 serve 的职责。
//
//	app validate --config configs/config.yaml --strict
func buildValidateCmd(opts []di.Option) *cobra.Command {
	var strict bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Load config, run module config validators, and report unused keys",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := newApp(cmd, opts)
			defer root.Shutdown()

			// 只解析 *config.Config：它会按配置树加载文件/Consul，
			// 但不会触碰任何外部依赖。
			cfg, err := di.Invoke[*config.Config](root)
			if err != nil {
				return fmt.Errorf("validate: load config: %w", err)
			}

			// 逐段跑各模块的 Load（含 SetDefaults + Validate）。
			// 这是纯内存操作：结构体校验，不连接外部服务。
			if err := runConfigValidators(root); err != nil {
				return fmt.Errorf("validate: %w", err)
			}

			unused := cfg.Unused()
			if len(unused) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "config ok: no unused keys")
				return nil
			}

			for _, u := range unused {
				fmt.Fprintf(cmd.ErrOrStderr(), "unused config key: %-40s %s\n", u.Key, u.Reason)
			}
			if strict {
				return fmt.Errorf("validate: %d unused config key(s) (--strict)", len(unused))
			}
			slog.Warn("config has unused keys (not fatal without --strict)", "count", len(unused))
			return nil
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero when unused config keys are found")
	return cmd
}

// runConfigValidators 触发所有已登记配置段的 Load/Validate。
//
// 依赖 config.RegisterConsumer 的登记信息：每个调用过 config.Load[T](key)
// 的模块都会留下记录，这里按记录重新解析一遍以执行其 Validate()。
func runConfigValidators(root di.Injector) error {
	return config.RunRegisteredValidators(root)
}
