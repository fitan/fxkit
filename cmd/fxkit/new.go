package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newCmd() *cobra.Command {
	var (
		minimal  bool
		skipTidy bool
		outDir   string
	)

	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Add a service under services/<name> in the current monorepo",
		Args:  cobra.ExactArgs(1),
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

			target := outDir
			if target == "" {
				target = filepath.Join(repoRoot, "services", name)
			}
			if err := ensureDir(target); err != nil {
				return err
			}

			bundle := "default"
			if minimal {
				bundle = "minimal"
			}
			data := scaffoldData{
				AppName: name,
				Module:  modulePath,
				Minimal: minimal,
			}

			fmt.Printf("scaffolding service %s → %s (module=%s, bundle=%s)\n",
				name, target, modulePath, bundle)
			if err := renderTemplate("templates/service", target, data); err != nil {
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
	return cmd
}

func refuseFxkitLibrary(modulePath, outDir string) error {
	if outDir == "" && modulePath == "github.com/fitan/fxkit" {
		return fmt.Errorf("refusing to add a service to the fxkit library repo; run `fxkit init <name>` first (or pass --out)")
	}
	return nil
}
