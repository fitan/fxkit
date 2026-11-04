package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

func initCmd() *cobra.Command {
	var (
		modulePath string
		skipTidy   bool
		outDir     string
		fxkitPath  string
		force      bool
	)

	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Create a monorepo (single go.mod, services/ layout)",
		Long: `Create a monorepo (single go.mod, services/ layout).

The generated go.mod requires a concrete fxkit version. It is resolved in this order:
  1. FXKIT_MODULE_VERSION (e.g. FXKIT_MODULE_VERSION=v1.2.3 fxkit init app)
  2. the version injected at build time with -ldflags "-X main.fxkitModuleVersionInjected=..."
  3. the version recorded in this binary's build info (when installed with ` + "`go install ...@vX.Y.Z`" + `)

If none is available, init fails instead of writing an unusable go.mod:
pass --fxkit-path /path/to/fxkit (adds a ` + "`replace`" + `) or set FXKIT_MODULE_VERSION.

` + "`go mod tidy`" + ` runs afterwards; if it fails, init exits non-zero (use --skip-tidy to skip it).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := validateName("repo", name); err != nil {
				return err
			}
			if modulePath == "" {
				modulePath = "example.com/" + name
			}

			absFxkit, err := resolveFxkitPath(fxkitPath)
			if err != nil {
				return err
			}

			data := scaffoldData{
				AppName:   name,
				Module:    modulePath,
				FxkitPath: absFxkit,
			}
			if absFxkit == "" {
				// 先解析版本：失败时不应该留下任何文件。
				version, err := resolveFxkitVersion()
				if err != nil {
					return err
				}
				data.FxkitVersion = version
			}

			target := outDir
			if target == "" {
				target = filepath.Join(".", name)
			}
			if err := ensureWritableDir(target, force); err != nil {
				return err
			}

			fmt.Printf("scaffolding repo %s → %s (module=%s)\n", name, target, modulePath)
			if err := renderTemplateWith("templates/repo", target, data, renderOptions{Force: force}); err != nil {
				return err
			}
			if skipTidy {
				return nil
			}
			return runGoModTidy(target)
		},
	}

	cmd.Flags().StringVar(&modulePath, "module", "", "Go module path (default: example.com/<name>)")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default: ./<name>)")
	cmd.Flags().BoolVar(&skipTidy, "skip-tidy", false, "skip the post-generation go mod tidy")
	cmd.Flags().StringVar(&fxkitPath, "fxkit-path", "", "local fxkit path to inject as a replace directive (useful in monorepo dev)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files (.gitignore is merged, never replaced)")
	return cmd
}
