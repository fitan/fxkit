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
	)

	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Create a monorepo (single go.mod, services/ layout)",
		Args:  cobra.ExactArgs(1),
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

			target := outDir
			if target == "" {
				target = filepath.Join(".", name)
			}
			if err := ensureWritableDir(target); err != nil {
				return err
			}

			data := scaffoldData{
				AppName:   name,
				Module:    modulePath,
				FxkitPath: absFxkit,
			}

			fmt.Printf("scaffolding repo %s → %s (module=%s)\n", name, target, modulePath)
			if err := renderTemplate("templates/repo", target, data); err != nil {
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
	cmd.Flags().StringVar(&fxkitPath, "fxkit-path", "", "local fxkit path to inject as `replace` (useful in monorepo dev)")
	return cmd
}
