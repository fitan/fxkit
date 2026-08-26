package main

import (
	"bufio"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"unicode"
)

//go:embed templates
var templatesFS embed.FS

// scaffoldData 填充模板文件中的 {{.Foo}} 占位符。
type scaffoldData struct {
	AppName   string // init：仓库名；new：服务名
	Module    string // 仓库根 go.mod 的 module 路径
	Minimal   bool   // new --minimal → fxkit.Minimal()
	FxkitPath string // 可选本地路径，注入为 replace
}

func validateName(kind, name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("%s name is required", kind)
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("%s name %q must not contain path separators", kind, name)
	}
	for _, r := range name {
		if unicode.IsSpace(r) {
			return fmt.Errorf("%s name %q must not contain whitespace", kind, name)
		}
	}
	return nil
}

func resolveFxkitPath(fxkitPath string) (string, error) {
	if fxkitPath == "" {
		return "", nil
	}
	abs, err := filepath.Abs(fxkitPath)
	if err != nil {
		return "", fmt.Errorf("resolve fxkit path: %w", err)
	}
	return abs, nil
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

func runGoModTidy(dir string) error {
	fmt.Println("running `go mod tidy`...")
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Stdout = os.Stdout
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: go mod tidy failed; run it manually:", err)
	}
	return nil
}

func findRepoRoot(start string) (root, modulePath string, err error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			mod, err := parseModulePath(candidate)
			if err != nil {
				return "", "", err
			}
			return dir, mod, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", errors.New("no go.mod found; run `fxkit init <name>` first")
		}
		dir = parent
	}
}

func parseModulePath(goMod string) (string, error) {
	f, err := os.Open(goMod)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "module ") {
			mod := strings.TrimSpace(strings.TrimPrefix(line, "module"))
			if mod == "" {
				return "", fmt.Errorf("empty module path in %s", goMod)
			}
			return mod, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no module line in %s", goMod)
}

func ensureDir(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("target directory %q already exists", path)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}
	return nil
}

// ensureWritableDir creates path if missing, or accepts an existing directory that
// only contains ignorable VCS files (so `fxkit init foo --out .` works in a fresh git repo).
func ensureWritableDir(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create target dir: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("target %q exists and is not a directory", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		switch e.Name() {
		case ".git", ".gitignore", ".DS_Store":
			continue
		default:
			return fmt.Errorf("target directory %q is not empty", path)
		}
	}
	return nil
}
