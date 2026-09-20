package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/openapi"
	"github.com/fitan/fxkit/server"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

func buildOpenAPICmd(opts []fx.Option) *cobra.Command {
	var (
		format string
		spec   string
		out    string
		base   string
	)
	cmd := &cobra.Command{
		Use:   "openapi",
		Short: "Print the application OpenAPI specification to stdout or a file",
		Long: `Starts the Fx graph without listening on HTTP, captures the Huma OpenAPI
document, and encodes it as YAML or JSON.

Supported --spec versions:
  3.0  OpenAPI 3.0.3 (default; recommended for oapi-codegen and older generators)
  3.1  OpenAPI 3.1.0 (native Huma / JSON Schema dialect)

Supported --format values:
  yaml (default)
  json

Merge static YAML (e.g. from an existing legacy swagger) using --base path.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ver := openapi.Spec30
			if strings.TrimSpace(spec) == "3.1" {
				ver = openapi.Spec31
			}
			fmtVal := openapi.FormatYAML
			if strings.EqualFold(strings.TrimSpace(format), "json") {
				fmtVal = openapi.FormatJSON
			}
			var baseYAML []byte
			if strings.TrimSpace(base) != "" {
				var err error
				baseYAML, err = os.ReadFile(strings.TrimSpace(base))
				if err != nil {
					return fmt.Errorf("openapi --base: %w", err)
				}
			}
			cfg := config.Options{
				ConfigFile:      flagString(cmd, "config"),
				ConsulAddress:   flagString(cmd, "consul"),
				ConsulConfigKey: flagString(cmd, "consul-key"),
				Port:            flagString(cmd, "port"),
			}
			body, err := collectOpenAPI(opts, cfg, openapi.EncodeInput{
				Version: ver,
				Format:  fmtVal,
				Base:    baseYAML,
			})
			if err != nil {
				return err
			}
			if out == "" || out == "-" {
				_, err = cmd.OutOrStdout().Write(body)
				return err
			}
			if err := os.WriteFile(out, body, 0o644); err != nil {
				return fmt.Errorf("openapi write %s: %w", out, err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "yaml", "output format: yaml or json")
	cmd.Flags().StringVar(&spec, "spec", "3.0", "OpenAPI version: 3.0 (oapi-codegen) or 3.1")
	cmd.Flags().StringVarP(&out, "output", "o", "", "write spec to file instead of stdout")
	cmd.Flags().StringVar(&base, "base", "", "optional static OpenAPI YAML/JSON to merge under Huma")
	return cmd
}

func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

// collectOpenAPI starts the host Fx graph without binding HTTP, then encodes
// the live Huma OpenAPI document. Used by the `openapi` command and tests.
func collectOpenAPI(opts []fx.Option, cfg config.Options, in openapi.EncodeInput) ([]byte, error) {
	var api huma.API
	app := fx.New(append([]fx.Option{
		fx.StartTimeout(defaultStartTimeout),
		fx.StopTimeout(defaultStopTimeout),
		fx.NopLogger,
		fx.Supply(cfg),
		fx.Supply(&server.Runtime{SkipListen: true}),
		fx.Invoke(func(p struct {
			fx.In
			API huma.API `optional:"true"`
		}) {
			api = p.API
		}),
	}, opts...)...)
	if err := app.Err(); err != nil {
		return nil, fmt.Errorf("openapi: fx graph: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultStartTimeout)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		if api != nil {
			slog.Warn("openapi: app.Start failed but huma.API is available; generating spec offline", "error", err)
			return openapi.Encode(api, in)
		}
		return nil, fmt.Errorf("openapi: start: %w", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), defaultStopTimeout)
		defer stopCancel()
		_ = app.Stop(stopCtx)
	}()

	if api == nil {
		return nil, fmt.Errorf("openapi: no Huma API in the Fx graph; add fxhuma.Module to fxkit.Run")
	}
	return openapi.Encode(api, in)
}
