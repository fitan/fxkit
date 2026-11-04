package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newCmd() *cobra.Command {
	var (
		minimal  bool
		skipTidy bool
		outDir   string
		force    bool
	)

	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Add a service under services/<name> in the current monorepo",
		Long: `Add a service to the current monorepo.

The service import path is derived from its real location relative to the module root,
so --out ./internal/api generates "example.com/app/internal/api/internal/hello"
(not the hardcoded services/<name> path).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := validateName("service", name); err != nil {
				return err
			}

			cwd, err := filepath.Abs(".")
			if err != nil {
				return err
			}
			repoRoot, modulePath, err := findRepoRoot(cwd)
			if err != nil {
				return err
			}
			if err := refuseFxkitLibrary(modulePath, outDir); err != nil {
				return err
			}

			data, target, err := newServiceScaffold(repoRoot, modulePath, name, outDir, minimal)
			if err != nil {
				return err
			}
			if force {
				// --force：允许在已有服务目录上重新生成（逐文件的覆盖策略见 renderTemplateWith）。
				if err := os.MkdirAll(target, 0o755); err != nil {
					return fmt.Errorf("create target dir: %w", err)
				}
			} else if err := ensureDir(target); err != nil {
				return err
			}

			bundle := "default"
			if minimal {
				bundle = "minimal"
			}

			fmt.Printf("scaffolding service %s → %s (module=%s, bundle=%s)\n",
				name, target, modulePath, bundle)
			if err := renderTemplateWith("templates/service", target, data, renderOptions{Force: force}); err != nil {
				return err
			}
			if skipTidy {
				return nil
			}
			return runGoModTidy(repoRoot)
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default: <repo>/services/<name>)")
	cmd.Flags().BoolVar(&minimal, "minimal", false, "scaffold fxkit.Minimal() + Huma (HTTP + docs only)")
	cmd.Flags().BoolVar(&skipTidy, "skip-tidy", false, "skip the post-generation go mod tidy")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files in the target directory")
	return cmd
}

func refuseFxkitLibrary(modulePath, outDir string) error {
	if outDir == "" && modulePath == "github.com/fitan/fxkit" {
		return fmt.Errorf("refusing to add a service to the fxkit library repo; run `fxkit init <name>` first (or pass --out)")
	}
	return nil
}

// newServiceScaffold 计算服务模板数据与输出目录。
// import path 取自 target 相对 module root 的真实位置，而不是硬编码 services/<name>。
func newServiceScaffold(repoRoot, modulePath, name, outDir string, minimal bool) (scaffoldData, string, error) {
	target := outDir
	if target == "" {
		target = filepath.Join(repoRoot, "services", name)
	}
	rel, err := relativeToModule(repoRoot, target)
	if err != nil {
		return scaffoldData{}, "", err
	}
	return scaffoldData{
		AppName:       name,
		Module:        modulePath,
		Minimal:       minimal,
		ImportPath:    modulePath + "/" + rel,
		ServiceDir:    rel,
		DefaultLayout: rel == "services/"+name,
	}, target, nil
}
