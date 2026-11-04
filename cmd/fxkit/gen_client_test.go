package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSpec = "openapi: 3.0.3\ninfo:\n  title: t\n  version: 1.0.0\npaths: {}\n"

func writeTestSpec(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "in.yaml")
	if err := os.WriteFile(path, []byte(testSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileContents(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestGenerateClientKeepsUserEdits 是回归测试：重复运行 `fxkit gen client`
// 曾经静默覆盖用户手改过的 reqx.go / generate.go。
func TestGenerateClientKeepsUserEdits(t *testing.T) {
	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	out := filepath.Join(dir, "users")

	in := clientGenInput{SpecPath: specPath, OutDir: out, SkipCodegen: true}
	if err := generateClient(in); err != nil {
		t.Fatal(err)
	}

	reqxPath := filepath.Join(out, "reqx.go")
	genPath := filepath.Join(out, "generate.go")
	const reqxEdit = "// hand-edited\npackage users\n"
	const genEdit = "// hand-edited go:generate\npackage users\n"
	if err := os.WriteFile(reqxPath, []byte(reqxEdit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(genPath, []byte(genEdit), 0o644); err != nil {
		t.Fatal(err)
	}

	// 再次生成（不带 --force）：用户文件必须原样保留。
	if err := generateClient(in); err != nil {
		t.Fatal(err)
	}
	if got := fileContents(t, reqxPath); got != reqxEdit {
		t.Errorf("reqx.go was overwritten:\n%s", got)
	}
	if got := fileContents(t, genPath); got != genEdit {
		t.Errorf("generate.go was overwritten:\n%s", got)
	}
	// 生成物仍然刷新。
	if got := fileContents(t, filepath.Join(out, "openapi.yaml")); !strings.Contains(got, "openapi: 3.0.3") {
		t.Errorf("openapi.yaml not refreshed:\n%s", got)
	}

	// --force 才覆盖。
	in.Force = true
	if err := generateClient(in); err != nil {
		t.Fatal(err)
	}
	if got := fileContents(t, reqxPath); strings.Contains(got, "hand-edited") {
		t.Errorf("reqx.go should be regenerated with --force:\n%s", got)
	}
	if !strings.Contains(fileContents(t, reqxPath), "NewFromFactory") {
		t.Error("reqx.go lost its generated content")
	}
}

// TestGenerateClientCodegenFailureLeavesNothing 校验 staging 目录策略：
// oapi-codegen 失败时不应创建/污染 --out。
func TestGenerateClientCodegenFailureLeavesNothing(t *testing.T) {
	orig := runCodegen
	runCodegen = func(string) error { return errors.New("boom") }
	t.Cleanup(func() { runCodegen = orig })

	dir := t.TempDir()
	specPath := writeTestSpec(t, dir)
	out := filepath.Join(dir, "users")

	err := generateClient(clientGenInput{SpecPath: specPath, OutDir: out})
	if err == nil {
		t.Fatal("expected codegen failure")
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("out dir must not be created on failure: %v", statErr)
	}
	assertNoStagingLeftovers(t, dir)

	// 已存在的 outDir 也不应被改动。
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(out, "reqx.go")
	if err := os.WriteFile(keep, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := generateClient(clientGenInput{SpecPath: specPath, OutDir: out}); err == nil {
		t.Fatal("expected codegen failure")
	}
	if got := fileContents(t, keep); got != "keep me\n" {
		t.Errorf("existing file was modified on failure: %q", got)
	}
	assertNoStagingLeftovers(t, dir)
}

func assertNoStagingLeftovers(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".fxkit-client-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("staging dirs left behind: %v", matches)
	}
}
