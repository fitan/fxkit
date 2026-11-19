package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/openapi"
	"github.com/fitan/fxkit/server"
	"github.com/spf13/cobra"
)

// buildOpenAPICmd 构建 openapi 子命令：编码 Huma 运行期 spec 并写到 stdout 或 --output 文件。
// --base 提供静态 YAML/JSON 与 Huma 文档合并；--best-effort 在启动失败时输出不完整 spec。
func buildOpenAPICmd(opts []di.Option) *cobra.Command {
	var (
		format     string
		spec       string
		out        string
		base       string
		bestEffort bool
	)
	cmd := &cobra.Command{
		Use:   "openapi",
		Short: "Print the application OpenAPI specification to stdout or a file",
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
			if strings.TrimSpace(base) != "" {
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
			}, bestEffort)
			if err != nil {
				return err
			}
			if out == "" || out == "-" {
				_, err = cmd.OutOrStdout().Write(body)
				return err
			}
			if err := writeFileAtomic(out, body, 0o644); err != nil {
				return fmt.Errorf("openapi write %s: %w", out, err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "yaml", "output format: yaml or json")
	cmd.Flags().StringVar(&spec, "spec", "3.0", "OpenAPI version: 3.0 / 3.0.3 (oapi-codegen) or 3.1 / 3.1.0")
	cmd.Flags().StringVarP(&out, "output", "o", "", "write spec to file instead of stdout")
	cmd.Flags().StringVar(&base, "base", "", "optional static OpenAPI YAML/JSON to merge under Huma")
	cmd.Flags().BoolVar(&bestEffort, "best-effort", false, "emit a partial spec even if startup fails")
	return cmd
}

// flagString 读取字符串 flag；flag 不存在时返回空串。
func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

// collectOpenAPI 在不绑定 HTTP 的前提下启动宿主对象图，然后编码
// 运行期的 Huma OpenAPI 文档。
func collectOpenAPI(opts []di.Option, cfg config.Options, in openapi.EncodeInput, bestEffort bool) ([]byte, error) {
	supplyOpt := func(i di.Injector) {
		di.ProvideValue(i, cfg)
		di.ProvideValue(i, &server.Runtime{SkipListen: true})
	}
	all := append([]di.Option{supplyOpt}, opts...)
	root := di.New(all...)
	defer root.Shutdown()

	lc := di.MustInvoke[*di.Lifecycle](root)
	if lc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), defaultStartTimeout)
		defer cancel()
		if err := lc.Start(ctx); err != nil {
			// 启动失败通常意味着 registrar 没跑完，此时输出的 spec 往往缺 paths。
			// 默认直接失败，只有显式 --best-effort 才退化为“尽力而为”。
			if !bestEffort {
				return nil, fmt.Errorf("openapi: start: %w (use --best-effort to emit a partial spec)", err)
			}
			if api, apiErr := di.Invoke[huma.API](root); apiErr == nil && api != nil {
				slog.Warn("openapi: start failed; emitting partial spec", "error", err)
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

	api, err := di.Invoke[huma.API](root)
	if err != nil || api == nil {
		return nil, fmt.Errorf("openapi: no Huma API in the di graph; add huma.Module to Run")
	}
	return openapi.Encode(api, in)
}

// writeFileAtomic 写入临时文件后 rename，避免中断留下半截文件。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".fxkit-openapi-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
