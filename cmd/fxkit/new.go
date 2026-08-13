package main

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

//go:embed templates
var templatesFS embed.FS

// scaffoldData 填充模板文件中的 {{.Foo}} 占位符。
type scaffoldData struct {
	AppName     string // 可读名称（也是二进制名）
	Module      string // go module 路径
	UseDapr     bool   // 除非 --no-dapr 或 --minimal，否则为 true
	UseDB       bool   // 除非 --no-db 或 --minimal，否则为 true
	UseAuthz    bool   // 跟随 --no-dapr（RBAC 默认随 Dapr 栈提供）
	UseConsul   bool   // 跟随 --no-dapr
	UseWorkflow bool   // 跟随 --no-dapr
	FxkitPath   string // 可选本地路径，注入为 `replace github.com/fitan/fxkit => <path>`
}

func newCmd() *cobra.Command {
	var (
		modulePath string
		minimal    bool
		noDapr     bool
		noDB       bool
		skipTidy   bool
		outDir     string
		fxkitPath  string
	)

	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Scaffold a new fxkit project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == "" {
				return errors.New("project name is required")
			}
			if modulePath == "" {
				modulePath = "example.com/" + name
			}

			absFxkit := ""
			if fxkitPath != "" {
				abs, err := filepath.Abs(fxkitPath)
				if err != nil {
					return fmt.Errorf("resolve fxkit path: %w", err)
				}
				absFxkit = abs
			}

			data := scaffoldData{
				AppName:     name,
				Module:      modulePath,
				UseDapr:     !minimal && !noDapr,
				UseDB:       !minimal && !noDB,
				UseAuthz:    !minimal && !noDapr,
				UseConsul:   !minimal && !noDapr,
				UseWorkflow: !minimal && !noDapr,
				FxkitPath:   absFxkit,
			}

			templateRoot := "templates/default"
			if minimal {
				templateRoot = "templates/minimal"
			}

			target := outDir
			if target == "" {
				target = filepath.Join(".", name)
			}
			if _, err := os.Stat(target); err == nil {
				return fmt.Errorf("target directory %q already exists", target)
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create target dir: %w", err)
			}

			fmt.Printf("scaffolding %s → %s (module=%s, template=%s)\n",
				name, target, modulePath, filepath.Base(templateRoot))

			if err := renderTemplate(templateRoot, target, data); err != nil {
				return err
			}

			if skipTidy {
				return nil
			}
			fmt.Println("running `go mod tidy`...")
			tidy := exec.Command("go", "mod", "tidy")
			tidy.Dir = target
			tidy.Stdout = os.Stdout
			tidy.Stderr = os.Stderr
			if err := tidy.Run(); err != nil {
				fmt.Fprintln(os.Stderr, "warning: go mod tidy failed; run it manually:", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&modulePath, "module", "", "Go module path (default: example.com/<name>)")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default: ./<name>)")
		cmd.Flags().BoolVar(&minimal, "minimal", false, "scaffold minimal template (HTTP + docs + server only)")
	cmd.Flags().BoolVar(&noDapr, "no-dapr", false, "omit Dapr resources from the default template")
	cmd.Flags().BoolVar(&noDB, "no-db", false, "omit gormx config from the default template")
	cmd.Flags().BoolVar(&skipTidy, "skip-tidy", false, "skip the post-generation go mod tidy")
	cmd.Flags().StringVar(&fxkitPath, "fxkit-path", "", "local fxkit path to inject as `replace` (useful in monorepo dev)")
	return cmd
}

func renderTemplate(root, target string, data scaffoldData) error {
	return fs.WalkDir(templatesFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		outPath := filepath.Join(target, rel)
		outPath = strings.TrimSuffix(outPath, ".tmpl")
		// 约定：名为 "gitignore"（或 "dot.gitignore"）的文件会改写为 ".gitignore"，
		// 以便模板在不同操作系统/编辑器下避开隐藏文件问题。
		if base := filepath.Base(outPath); base == "gitignore" || base == "dot.gitignore" {
			outPath = filepath.Join(filepath.Dir(outPath), ".gitignore")
		}

		if d.IsDir() {
			return os.MkdirAll(outPath, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}

		raw, err := templatesFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read template %s: %w", path, err)
		}

		mode := os.FileMode(0o644)

		if !strings.HasSuffix(path, ".tmpl") {
			return os.WriteFile(outPath, raw, mode)
		}

		tmpl, err := template.New(rel).Parse(string(raw))
		if err != nil {
			return fmt.Errorf("parse template %s: %w", path, err)
		}
		f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := tmpl.Execute(f, data); err != nil {
			return fmt.Errorf("execute template %s: %w", path, err)
		}
		return nil
	})
}
