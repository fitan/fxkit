package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/mcpx"
	"github.com/fitan/fxkit/server"
	"github.com/spf13/cobra"
)

func buildMCPCmd(opts []di.Option) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the service as an in-process Model Context Protocol (MCP) server over standard I/O",
		RunE: func(cmd *cobra.Command, _ []string) error {
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: slog.LevelWarn,
			})))

			cfg := config.Options{
				ConfigFile:      flagString(cmd, "config"),
				ConsulAddress:   flagString(cmd, "consul"),
				ConsulConfigKey: flagString(cmd, "consul-key"),
			}

			supplyOpt := func(i di.Injector) {
				di.ProvideValue(i, cfg)
				di.ProvideValue(i, &server.Runtime{SkipListen: true})
			}
			all := append([]di.Option{supplyOpt}, opts...)
			root := di.New(all...)
			defer root.Shutdown()

			srv, _ := di.Invoke[*mcpx.Server](root)

			lc, _ := di.Invoke[*di.Lifecycle](root)
			if lc != nil {
				startCtx, cancel := context.WithTimeout(context.Background(), defaultStartTimeout)
				defer cancel()
				if err := lc.Start(startCtx); err != nil {
					return fmt.Errorf("mcp: start app failed: %w", err)
				}
				defer func() {
					stopCtx, stopCancel := context.WithTimeout(context.Background(), defaultStopTimeout)
					defer stopCancel()
					_ = lc.Stop(stopCtx)
				}()
			}

			if srv == nil {
				srv, _ = di.Invoke[*mcpx.Server](root)
			}
			if srv == nil {
				return fmt.Errorf("mcp: no *mcpx.Server found in di graph; ensure mcpx.Module is included in Run")
			}

			return srv.ServeStdio()
		},
	}
	return cmd
}
