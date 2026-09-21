package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/mcpx"
	"github.com/fitan/fxkit/server"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

func buildMCPCmd(opts []fx.Option) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the service as an in-process Model Context Protocol (MCP) server over standard I/O",
		Long: `Starts the application graph without binding HTTP listen ports, automatically exposes
all Huma-registered operations as MCP Tools, and serves over standard I/O for
Claude Desktop, Cursor, and LLM Agents.

In claude_desktop_config.json:
  {
    "mcpServers": {
      "myservice": {
        "command": "/path/to/myservice",
        "args": ["mcp", "--config", "configs/config.yaml"]
      }
    }
  }`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// MCP Stdio 模式下，所有应用/框架日志重定向到 stderr，防止污染 stdout 协议流
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: slog.LevelWarn,
			})))

			cfg := config.Options{
				ConfigFile:      flagString(cmd, "config"),
				ConsulAddress:   flagString(cmd, "consul"),
				ConsulConfigKey: flagString(cmd, "consul-key"),
			}

			var srv *mcpx.Server
			app := fx.New(append([]fx.Option{
				fx.StartTimeout(defaultStartTimeout),
				fx.StopTimeout(defaultStopTimeout),
				fx.NopLogger,
				fx.Supply(cfg),
				fx.Supply(&server.Runtime{SkipListen: true}),
				fx.Invoke(func(p struct {
					fx.In
					Server *mcpx.Server `optional:"true"`
				}) {
					srv = p.Server
				}),
			}, opts...)...)

			if err := app.Err(); err != nil {
				return fmt.Errorf("mcp: fx graph error: %w", err)
			}

			startCtx, cancel := context.WithTimeout(context.Background(), defaultStartTimeout)
			defer cancel()
			if err := app.Start(startCtx); err != nil {
				return fmt.Errorf("mcp: start app failed: %w", err)
			}
			defer func() {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), defaultStopTimeout)
				defer stopCancel()
				_ = app.Stop(stopCtx)
			}()

			if srv == nil {
				return fmt.Errorf("mcp: no *mcpx.Server found in Fx graph; ensure mcpx.Module is included in fxkit.Run")
			}

			return srv.ServeStdio()
		},
	}
	return cmd
}
