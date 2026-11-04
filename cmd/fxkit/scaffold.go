package main

import (
	"bufio"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"text/template"
	"unicode"
)

//go:embed templates
var templatesFS embed.FS

// fxkitModuleVersionInjected 可通过
//
//	go build -ldflags "-X main.fxkitModuleVersionInjected=v1.2.3" ./cmd/fxkit
//
// 注入。发布二进制时请设置，否则用户必须先设 FXKIT_MODULE_VERSION 或传 --fxkit-path。
var fxkitModuleVersionInjected string

// funcs 是模板可用的辅助函数。
//
// quote 用 strconv.Quote 转义所有插入 Go 字符串字面量的值，避免模板注入
// （例如服务名/表名里带引号时生成非法源码）。
var funcs = template.FuncMap{
	"quote": strconv.Quote,
}

// scaffoldData 填充模板文件中的 {{.Foo}} 占位符。
type scaffoldData struct {
	AppName       string // init：仓库名；new：服务名
	Module        string // 仓库根 go.mod 的 module 路径
	Minimal       bool   // new --minimal → fxkit.Minimal()
	FxkitPath     string // 可选本地路径，注入为 replace
	FxkitVersion  string // init 生成的 go.mod 里 fxkit 的版本（--fxkit-path 时为空）
	ImportPath    string // new：服务包相对 module root 的 import path
	ServiceDir    string // new：服务目录相对 module root（slash 分隔）
	DefaultLayout bool   // new：是否落在默认的 services/<name> 布局
}

// moduleVersionRE 匹配合法的 Go module 版本（含伪版本 v0.0.0-<ts>-<hash>）。
var moduleVersionRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.\-]+)?(\+[0-9A-Za-z.\-]+)?$`)

// validateModuleVersion 拒绝 "latest"、"" 这类 go.mod 里非法的版本约束。
func validateModuleVersion(v string) error {
	if !moduleVersionRE.MatchString(v) {
		return fmt.Errorf("invalid Go module version %q (want a semver tag like v1.2.3 or a pseudo-version)", v)
	}
	return nil
}

// fxkitModuleVersion 返回 init 生成的 go.mod 里 fxkit 的版本约束。
//
// 优先级：FXKIT_MODULE_VERSION 环境变量 > 编译期 -ldflags 注入 > build info 里的本模块版本。
// 拿不到合法版本时返回错误 —— 绝不写 "latest"（go.mod 里非法，会让用户
// 在 `go mod tidy` 阶段才发现问题）。
func fxkitModuleVersion() (string, error) {
	candidates := []struct{ source, value string }{
		{"FXKIT_MODULE_VERSION", os.Getenv("FXKIT_MODULE_VERSION")},
		{"fxkitModuleVersionInjected", fxkitModuleVersionInjected},
		{"build info", buildInfoModuleVersion()},
	}
	for _, c := range candidates {
		v := strings.TrimSpace(c.value)
		if v == "" {
			continue
		}
		if err := validateModuleVersion(v); err != nil {
			return "", fmt.Errorf("%s: %w", c.source, err)
		}
		return v, nil
	}
	return "", errors.New("no fxkit module version available")
}

// buildInfoModuleVersion 从 Go 构建信息读取本模块版本。
// `go install ...@vX.Y.Z` 安装的二进制会带上真实版本；本地 `go build` 则是 "(devel)"，
// 或者带 "+dirty" 的伪版本（工作区有未提交修改）——后者在 module proxy 上不存在，
// 写进 go.mod 只会重现「tidy 失败」的老问题，因此不算可用版本。
func buildInfoModuleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return ""
	}
	return usableModuleVersion(info.Main.Version)
}

// usableModuleVersion 过滤掉 build info 里不可用/不可解析的版本。
func usableModuleVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "(devel)" {
		return ""
	}
	// +dirty（或任何 dirty 构建元数据）表示本地有未提交修改，远端没有这个版本。
	if idx := strings.Index(v, "+"); idx >= 0 && strings.Contains(strings.ToLower(v[idx:]), "dirty") {
		return ""
	}
	if err := validateModuleVersion(v); err != nil {
		return ""
	}
	return v
}

// resolveFxkitVersion 给出用户可操作的错误信息。
func resolveFxkitVersion() (string, error) {
	v, err := fxkitModuleVersion()
	if err == nil {
		return v, nil
	}
	return "", fmt.Errorf(`cannot determine the fxkit version to require in go.mod: %w

Provide it explicitly, one of:
  - FXKIT_MODULE_VERSION=v1.2.3 fxkit init <name>
  - fxkit init <name> --fxkit-path /path/to/fxkit   (injects a local replace)
A binary built with -ldflags "-X main.fxkitModuleVersionInjected=vX.Y.Z" picks it up automatically.`, err)
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
		// 名字会被模板插进 Go/YAML 字面量；挡掉引号/反引号/控制字符，
		// 避免生成出语法错误或语义漂移的配置。
		if !unicode.IsPrint(r) || strings.ContainsRune("\"'`\\", r) {
			return fmt.Errorf("%s name %q must not contain quotes, backticks or control characters", kind, name)
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

// writeFileAtomic 先写同目录临时文件，fsync 后 rename，避免中断后留下半截文件。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename temp file to %s: %w", path, err)
	}
	return nil
}

// renderOptions 控制模板落盘的覆盖策略。
type renderOptions struct {
	// Force 允许覆盖已存在的文件。.gitignore 例外：始终按「追加缺失项」合并。
	Force bool
}

func renderTemplate(root, target string, data scaffoldData) error {
	return renderTemplateWith(root, target, data, renderOptions{})
}

// renderTemplateWith 把嵌入模板渲染到 target。
//
// 写盘前先统一 os.Stat 扫描冲突：只要有一个目标文件已存在（且未 --force），
// 就整体失败，避免写到一半才报错、留下半成品目录。
func renderTemplateWith(root, target string, data scaffoldData, opts renderOptions) error {
	conflicts, err := templateConflicts(root, target, opts.Force)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("refusing to overwrite existing file(s) %s (use --force to overwrite)",
			strings.Join(conflicts, ", "))
	}
	return writeTemplates(root, target, data)
}

func writeTemplates(root, target string, data scaffoldData) error {
	return fs.WalkDir(templatesFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel, err := templateOutPath(root, path)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			return os.MkdirAll(filepath.Join(target, rel), 0o755)
		}

		rel, err := templateOutPath(root, path)
		if err != nil {
			return err
		}
		outPath := filepath.Join(target, rel)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}

		raw, err := templatesFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read template %s: %w", path, err)
		}

		// .gitignore 采用合并策略：只追加缺失条目，绝不整体覆盖用户已有内容。
		if rel == ".gitignore" {
			if _, err := os.Stat(outPath); err == nil {
				return mergeGitignore(outPath, raw)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return writeFileAtomic(outPath, raw, 0o644)
		}

		if !strings.HasSuffix(path, ".tmpl") {
			return writeFileAtomic(outPath, raw, 0o644)
		}

		tmpl, err := template.New(rel).Funcs(funcs).Parse(string(raw))
		if err != nil {
			return fmt.Errorf("parse template %s: %w", path, err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("execute template %s: %w", path, err)
		}
		return writeFileAtomic(outPath, buf.Bytes(), 0o644)
	})
}

// templateConflicts 返回所有会被覆盖的已存在文件（相对路径，已排序）。
func templateConflicts(root, target string, force bool) ([]string, error) {
	if force {
		return nil, nil
	}
	var conflicts []string
	err := fs.WalkDir(templatesFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := templateOutPath(root, path)
		if err != nil {
			return err
		}
		// .gitignore 会合并而不是覆盖，因此不算冲突。
		if rel == ".gitignore" {
			return nil
		}
		if _, err := os.Stat(filepath.Join(target, rel)); err == nil {
			conflicts = append(conflicts, filepath.ToSlash(rel))
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	})
	return conflicts, err
}

// templateOutPath 计算模板在 target 中的相对输出路径。
func templateOutPath(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	outPath := strings.TrimSuffix(rel, ".tmpl")
	// 约定：名为 "gitignore"（或 "dot.gitignore"）的文件会改写为 ".gitignore"，
	// 以便模板在不同操作系统/编辑器下避开隐藏文件问题。
	if base := filepath.Base(outPath); base == "gitignore" || base == "dot.gitignore" {
		outPath = filepath.Join(filepath.Dir(outPath), ".gitignore")
	}
	return outPath, nil
}

// mergeGitignore 把模板里缺失的条目追加到已有 .gitignore，保留用户原有内容。
func mergeGitignore(path string, generated []byte) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(string(existing), "\n") {
		seen[strings.TrimSpace(line)] = true
	}

	var sb strings.Builder
	sb.WriteString(strings.TrimRight(string(existing), "\n"))
	if sb.Len() > 0 {
		sb.WriteString("\n")
	}
	added := 0
	for _, line := range strings.Split(string(generated), "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" || seen[entry] {
			continue
		}
		seen[entry] = true
		sb.WriteString(entry)
		sb.WriteString("\n")
		added++
	}
	if added == 0 {
		return nil
	}
	if err := writeFileAtomic(path, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("appended %d missing entry(ies) to %s\n", added, path)
	return nil
}

func runGoModTidy(dir string) error {
	fmt.Println("running `go mod tidy`...")
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Stdout = os.Stdout
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return fmt.Errorf("go mod tidy in %s: %w (fix the error above, or re-run with --skip-tidy)", dir, err)
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

// ensureWritableDir 在 path 缺失时创建它，或接受一个仅包含可忽略的
// VCS 文件的已存在目录（这样 `fxkit init foo --out .` 在全新的 git 仓库中也能工作）。
// force 时才允许直接使用非空目录。
func ensureWritableDir(path string, force bool) error {
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
	if force {
		return nil
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

// withinDir 报告 target 是否位于 root 之内（含 root 本身）。
func withinDir(root, target string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// relativeToModule 返回 target 相对 module root 的 slash 路径，并拒绝根目录本身或越界路径。
func relativeToModule(root, target string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if !withinDir(absRoot, absTarget) {
		return "", fmt.Errorf("%q is outside the current module root %q", target, absRoot)
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", fmt.Errorf("%q is the module root; choose a package directory (e.g. internal/foo)", target)
	}
	return filepath.ToSlash(rel), nil
}
