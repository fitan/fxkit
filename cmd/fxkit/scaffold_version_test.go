package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFxkitModuleVersionResolution 锁定版本来源优先级：
// FXKIT_MODULE_VERSION > -ldflags 注入 > build info，且绝不返回 "latest"。
func TestFxkitModuleVersionResolution(t *testing.T) {
	origInjected := fxkitModuleVersionInjected
	t.Cleanup(func() { fxkitModuleVersionInjected = origInjected })
	fxkitModuleVersionInjected = ""

	t.Setenv("FXKIT_MODULE_VERSION", "v1.2.3")
	if v, err := fxkitModuleVersion(); err != nil || v != "v1.2.3" {
		t.Fatalf("env version: got %q err=%v", v, err)
	}

	t.Setenv("FXKIT_MODULE_VERSION", "latest")
	if _, err := fxkitModuleVersion(); err == nil {
		t.Fatal("`latest` must be rejected: it is not a valid go.mod version")
	}

	t.Setenv("FXKIT_MODULE_VERSION", "")
	fxkitModuleVersionInjected = "v0.9.0"
	if v, err := fxkitModuleVersion(); err != nil || v != "v0.9.0" {
		t.Fatalf("injected version: got %q err=%v", v, err)
	}

	fxkitModuleVersionInjected = "not-a-version"
	if _, err := fxkitModuleVersion(); err == nil {
		t.Fatal("invalid injected version must be rejected")
	}

	// 本地 `go test` 通常没有 build info 版本（"(devel)"）。
	fxkitModuleVersionInjected = ""
	buildVersion := buildInfoModuleVersion()
	v, err := fxkitModuleVersion()
	if buildVersion == "" {
		if err == nil {
			t.Fatalf("expected an error when no version source is available, got %q", v)
		}
		if _, rerr := resolveFxkitVersion(); rerr == nil {
			t.Fatal("resolveFxkitVersion should return a helpful error")
		} else {
			for _, want := range []string{"FXKIT_MODULE_VERSION", "--fxkit-path"} {
				if !strings.Contains(rerr.Error(), want) {
					t.Errorf("error message should mention %q:\n%v", want, rerr)
				}
			}
		}
	} else if err != nil || v != buildVersion {
		t.Fatalf("build info version: got %q err=%v, want %q", v, err, buildVersion)
	}
}

func TestValidateModuleVersion(t *testing.T) {
	t.Parallel()
	valid := []string{"v1.2.3", "v0.0.1", "v2.0.0-rc.1", "v0.0.0-20240101000000-abcdef123456", "v1.0.0+incompatible"}
	for _, v := range valid {
		if err := validateModuleVersion(v); err != nil {
			t.Errorf("%q should be valid: %v", v, err)
		}
	}
	invalid := []string{"", "latest", "1.2.3", "v1.2", "v1.2.3.4", "v01.2.3", "v1.2.3-"}
	for _, v := range invalid {
		if err := validateModuleVersion(v); err == nil {
			t.Errorf("%q should be invalid", v)
		}
	}
}

func TestBuildInfoModuleVersionSkipsDevel(t *testing.T) {
	t.Parallel()
	// 本地测试二进制通常没有版本；只要不返回 "(devel)" 即可。
	if v := buildInfoModuleVersion(); v == "(devel)" {
		t.Fatal("buildInfoModuleVersion must not return (devel)")
	}
}

// TestUsableModuleVersion 锁定 build info 版本的可用性判断：
// 本地 dirty 构建的伪版本在 module proxy 上不存在，不能写进 go.mod。
func TestUsableModuleVersion(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                                   "",
		"(devel)":                            "",
		"v1.2.3":                             "v1.2.3",
		"v0.0.0-20240101000000-abcdef123456": "v0.0.0-20240101000000-abcdef123456",
		"v0.0.0-20240101000000-abcdef123456+dirty":        "",
		"v0.0.0-20240101000000-abcdef123456+incompatible": "v0.0.0-20240101000000-abcdef123456+incompatible",
		"not-a-version": "",
	}
	for in, want := range cases {
		if got := usableModuleVersion(in); got != want {
			t.Errorf("usableModuleVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWriteFileAtomic 校验内容/权限写入，且不残留临时文件。
func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := writeFileAtomic(path, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Fatalf("content = %q", b)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("perm = %o, want 640", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "file.txt" {
		t.Errorf("temp files left behind: %v", entries)
	}

	// 覆盖写
	if err := writeFileAtomic(path, []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "world" {
		t.Fatalf("overwrite content = %q", b)
	}
}

func repoScaffoldData() scaffoldData {
	return scaffoldData{AppName: "platform", Module: "github.com/me/platform", FxkitPath: "/tmp/fxkit"}
}

// TestRenderTemplateRefusesExistingFiles 校验写盘前的 os.Stat 检查：
// 已存在的文件默认不覆盖，失败时不应该留下半成品。
func TestRenderTemplateRefusesExistingFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	data := repoScaffoldData()
	if err := renderTemplate("templates/repo", dir, data); err != nil {
		t.Fatal(err)
	}

	goMod := filepath.Join(dir, "go.mod")
	original := "module changed-by-the-user\n"
	if err := os.WriteFile(goMod, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	err := renderTemplate("templates/repo", dir, data)
	if err == nil {
		t.Fatal("expected an error when target files already exist")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") || !strings.Contains(err.Error(), "go.mod") {
		t.Errorf("unexpected error: %v", err)
	}
	if b, _ := os.ReadFile(goMod); string(b) != original {
		t.Errorf("go.mod was overwritten:\n%s", b)
	}

	// --force 才覆盖
	if err := renderTemplateWith("templates/repo", dir, data, renderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(goMod); !strings.Contains(string(b), "module github.com/me/platform") {
		t.Errorf("go.mod not regenerated with --force:\n%s", b)
	}
}

// TestGitignoreMerge 是回归测试：`init --out .` 曾经整体覆盖已有 .gitignore。
func TestGitignoreMerge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	existing := "node_modules/\nbin/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	data := repoScaffoldData()
	if err := renderTemplate("templates/repo", dir, data); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.HasPrefix(got, "node_modules/") {
		t.Errorf("user entries must be preserved:\n%s", got)
	}
	for _, want := range []string{"node_modules/", "bin/", "tmp/", ".data/", "*.log", ".DS_Store"} {
		if strings.Count(got, want+"\n") != 1 {
			t.Errorf("%q should appear exactly once:\n%s", want, got)
		}
	}
}

// TestNewServiceScaffoldImportPath 是回归测试：`fxkit new --out ./internal/api`
// 曾硬编码 services/<name>，生成出错误的 import path。
func TestNewServiceScaffoldImportPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	data, target, err := newServiceScaffold(root, "example.com/app", "api", filepath.Join(root, "internal", "api"), false)
	if err != nil {
		t.Fatal(err)
	}
	if data.ImportPath != "example.com/app/internal/api" {
		t.Errorf("ImportPath = %q", data.ImportPath)
	}
	if data.ServiceDir != "internal/api" {
		t.Errorf("ServiceDir = %q", data.ServiceDir)
	}
	if filepath.Clean(target) != filepath.Join(root, "internal", "api") {
		t.Errorf("target = %q", target)
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := renderTemplate("templates/service", target, data); err != nil {
		t.Fatal(err)
	}
	mainSrc := fileContents(t, filepath.Join(target, "cmd", "main.go"))
	if !strings.Contains(mainSrc, `"example.com/app/internal/api/internal/hello"`) {
		t.Errorf("main.go has the wrong import path:\n%s", mainSrc)
	}
	if strings.Contains(mainSrc, "services/api") {
		t.Errorf("main.go still hardcodes services/<name>:\n%s", mainSrc)
	}
	readme := fileContents(t, filepath.Join(target, "README.md"))
	if !strings.Contains(readme, "go run ./internal/api/cmd") {
		t.Errorf("README should reference the real path:\n%s", readme)
	}

	// 默认布局保持不变。
	def, _, err := newServiceScaffold(root, "example.com/app", "orders", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if def.ImportPath != "example.com/app/services/orders" || def.ServiceDir != "services/orders" {
		t.Errorf("default layout: %q / %q", def.ImportPath, def.ServiceDir)
	}

	// module 之外的目标要拒绝。
	if _, _, err := newServiceScaffold(root, "example.com/app", "pwn", filepath.Join(root, "..", "pwn"), false); err == nil {
		t.Fatal("expected out-of-module target to be rejected")
	}
}

// TestRunGoModTidyPropagatesError 校验 tidy 失败不再只是打一条 warning。
func TestRunGoModTidyPropagatesError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// 语法错误的 go.mod：go 会立即失败，不需要网络。
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\nrequire (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGoModTidy(dir); err == nil {
		t.Fatal("expected go mod tidy failure to be returned")
	} else if !strings.Contains(err.Error(), "skip-tidy") {
		t.Errorf("error should point at --skip-tidy: %v", err)
	}
}

// TestInitScaffoldUsesConcreteVersion 校验生成的 go.mod 里是具体版本。
func TestInitScaffoldUsesConcreteVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	data := scaffoldData{AppName: "platform", Module: "github.com/me/platform", FxkitVersion: "v1.4.2"}
	if err := renderTemplate("templates/repo", dir, data); err != nil {
		t.Fatal(err)
	}
	mod := fileContents(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(mod, "require github.com/fitan/fxkit v1.4.2") {
		t.Fatalf("go.mod missing the concrete version:\n%s", mod)
	}
	if strings.Contains(mod, "latest") || strings.Contains(mod, "replace ") {
		t.Fatalf("unexpected go.mod content:\n%s", mod)
	}
}
