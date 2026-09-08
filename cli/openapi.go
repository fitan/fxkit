package cli

import (
	"context"
	"fmt"
	"os"
	"time"

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
		Short: "Print the live Huma OpenAPI spec (does not listen HTTP)",
		Long: `Start the same Fx graph as serve, skip the HTTP listener, and print the
Huma OpenAPI document to stdout (or --output).

Default is OpenAPI 3.0.3 YAML so oapi-codegen can consume it. Use --spec 3.1
for native Huma 3.1. Requires huma.Module in fxkit.Run.

Logs go to stderr so the spec can be piped:

  myapp openapi > openapi.yaml
  myapp openapi --spec 3.1 --format json -o openapi.json
  fxkit gen client --spec openapi.yaml --out ./internal/clients/users
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ver, err := openapi.ParseSpecVersion(spec)
			if err != nil {
				return err
			}
			fmtVal, err := openapi.ParseFormat(format)
			if err != nil {
				return err
			}
			var baseYAML []byte
			if base != "" {
				baseYAML, err = os.ReadFile(base)
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
		fx.StartTimeout(60 * time.Second),
		fx.StopTimeout(15 * time.Second),
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		return nil, fmt.Errorf("openapi: start: %w", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		_ = app.Stop(stopCtx)
	}()

	if api == nil {
		return nil, fmt.Errorf("openapi: no Huma API in the Fx graph; add fxhuma.Module to fxkit.Run")
	}
	return openapi.Encode(api, in)
}
