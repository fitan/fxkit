package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/openapi"
	"github.com/fitan/fxkit/server"
	"github.com/spf13/cobra"
)

func buildOpenAPICmd(opts []di.Option) *cobra.Command {
	var (
		format string
		spec   string
		out    string
		base   string
	)
	cmd := &cobra.Command{
		Use:   "openapi",
		Short: "Print the application OpenAPI specification to stdout or a file",
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

// collectOpenAPI starts the host graph without binding HTTP, then encodes
// the live Huma OpenAPI document.
func collectOpenAPI(opts []di.Option, cfg config.Options, in openapi.EncodeInput) ([]byte, error) {
	supplyOpt := func(i di.Injector) {
		di.ProvideValue(i, cfg)
		di.ProvideValue(i, &server.Runtime{SkipListen: true})
	}
	all := append([]di.Option{supplyOpt}, opts...)
	root := di.New(all...)
	defer root.Shutdown()

	api, _ := di.Invoke[huma.API](root)

	lc, _ := di.Invoke[*di.Lifecycle](root)
	if lc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), defaultStartTimeout)
		defer cancel()
		if err := lc.Start(ctx); err != nil {
			if api != nil {
				slog.Warn("openapi: start failed but huma.API is available; generating spec offline", "error", err)
				return openapi.Encode(api, in)
			}
			return nil, fmt.Errorf("openapi: start: %w", err)
		}
		defer func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), defaultStopTimeout)
			defer stopCancel()
			_ = lc.Stop(stopCtx)
		}()
	}

	if api == nil {
		api, _ = di.Invoke[huma.API](root)
	}
	if api == nil {
		return nil, fmt.Errorf("openapi: no Huma API in the di graph; add huma.Module to Run")
	}
	return openapi.Encode(api, in)
}
