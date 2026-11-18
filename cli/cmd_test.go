package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestBuildMigrateCmd_PrintSQLite 覆盖 `migrate print`：不连库，只读嵌入的 DDL。
func TestBuildMigrateCmd_PrintSQLite(t *testing.T) {
	cmd := buildMigrateCmd(nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"print", "--dialect", "sqlite"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"outbox_events", "casbin_rule", "authz_role"} {
		if !strings.Contains(got, want) {
			t.Fatalf("migrate print output missing %q:\n%s", want, got)
		}
	}
}

// TestBuildMigrateCmd_PrintUnknownDialect 非法方言必须报错，而不是输出空串。
func TestBuildMigrateCmd_PrintUnknownDialect(t *testing.T) {
	cmd := buildMigrateCmd(nil)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"print", "--dialect", "oracle"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("unknown dialect must fail")
	}
}

// TestBuildMigrateCmd_Baseline 覆盖 `migrate baseline`：写出框架 DDL，且默认不覆盖。
func TestBuildMigrateCmd_Baseline(t *testing.T) {
	dir := t.TempDir()
	cmd := buildMigrateCmd(nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"baseline", "--dialect", "sqlite", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "0001_fxkit_core.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"outbox_events", "idempotency_keys"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("baseline missing %q", want)
		}
	}

	// 第二次必须拒绝覆盖。
	cmd2 := buildMigrateCmd(nil)
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetArgs([]string{"baseline", "--dialect", "sqlite", "--dir", dir})
	if err := cmd2.Execute(); err == nil {
		t.Fatal("baseline must refuse to overwrite by default")
	}
}

// TestBuildVersionCmd 覆盖 `version` 的输出（含构建信息字段名）。
func TestBuildVersionCmd(t *testing.T) {
	cmd := buildVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)

	// version 用 fmt.Printf 直接写 stdout，这里抓 stdout。
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	_ = w.Close()
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	for _, want := range []string{"version:", "commit:", "go_version:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("version output missing %q: %s", want, got)
		}
	}
}

// TestFlagString 覆盖 flag 读取辅助函数。
func TestFlagString(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().String("dialect", "postgres", "")
	if got := flagString(cmd, "dialect"); got != "postgres" {
		t.Fatalf("got %q", got)
	}
	// 不存在的 flag 返回零值，不 panic。
	if got := flagString(cmd, "missing"); got != "" {
		t.Fatalf("missing flag should be empty, got %q", got)
	}
}

// TestWriteFileAtomic 覆盖原子写：内容与权限都正确，且不留临时文件。
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openapi.yaml")

	if err := writeFileAtomic(path, []byte("openapi: 3.1.0\n"), 0o640); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "openapi: 3.1.0\n" {
		t.Fatalf("content=%q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("perm=%v, want 0640", info.Mode().Perm())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %d entries", len(entries))
	}
}

// TestSetRootNameAndAddCommand 覆盖两个装配期配置入口。
func TestSetRootNameAndAddCommand(t *testing.T) {
	oldName, oldShort := rootName, rootShort
	oldExtra := extraCommands
	t.Cleanup(func() {
		rootName, rootShort = oldName, oldShort
		extraCommands = oldExtra
	})

	SetRootName("myapp", "my service")
	if rootName != "myapp" || rootShort != "my service" {
		t.Fatalf("name=%q short=%q", rootName, rootShort)
	}
	// 空值不覆盖已有值。
	SetRootName("", "")
	if rootName != "myapp" || rootShort != "my service" {
		t.Fatalf("empty values must not overwrite: %q / %q", rootName, rootShort)
	}

	extraCommands = nil
	AddCommand(&cobra.Command{Use: "a"}, &cobra.Command{Use: "b"})
	if len(extraCommands) != 2 {
		t.Fatalf("extraCommands=%d", len(extraCommands))
	}
}
