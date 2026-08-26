package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	t.Parallel()
	if err := validateName("service", "orders"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "foo/bar", "foo bar"} {
		if err := validateName("service", name); err == nil {
			t.Errorf("expected error for %q", name)
		}
	}
}

func TestParseModulePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(p, []byte("module github.com/me/platform\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := parseModulePath(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "github.com/me/platform" {
		t.Fatalf("got %q", got)
	}
}

func TestRefuseFxkitLibrary(t *testing.T) {
	t.Parallel()
	if err := refuseFxkitLibrary("github.com/fitan/fxkit", ""); err == nil {
		t.Fatal("expected refuse when scaffolding into the library module")
	}
	if err := refuseFxkitLibrary("github.com/fitan/fxkit", "/tmp/out"); err != nil {
		t.Fatal(err)
	}
	if err := refuseFxkitLibrary("github.com/me/platform", ""); err != nil {
		t.Fatal(err)
	}
}

func TestFindRepoRootWalksUp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "services", "x")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gotRoot, mod, err := findRepoRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != root {
		t.Fatalf("root: got %q want %q", gotRoot, root)
	}
	if mod != "example.com/app" {
		t.Fatalf("module: got %q", mod)
	}
}

func TestScaffoldRepoAndService(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	data := scaffoldData{
		AppName:   "platform",
		Module:    "github.com/me/platform",
		FxkitPath: "/tmp/fxkit",
	}
	if err := renderTemplate("templates/repo", dir, data); err != nil {
		t.Fatal(err)
	}

	mustExist(t, dir, "go.mod", "Makefile", "README.md", ".gitignore", "services/README.md",
		"docker-compose.yml", "deploy/postgres/init/01-hatchet.sql")
	mustNotExist(t, dir, "dapr.yaml", "configs/dapr")

	modBytes, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	mod := string(modBytes)
	if !strings.Contains(mod, "module github.com/me/platform") {
		t.Fatalf("go.mod missing module:\n%s", mod)
	}
	if !strings.Contains(mod, "replace github.com/fitan/fxkit => /tmp/fxkit") {
		t.Fatalf("go.mod missing replace:\n%s", mod)
	}
	if strings.Contains(strings.ToLower(mod), "dapr") {
		t.Fatal("go.mod mentions dapr")
	}

	composeBytes, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	compose := string(composeBytes)
	for _, want := range []string{"postgres:", "hatchet:", "consul:", "otel-lgtm:", "name: platform"} {
		if !strings.Contains(compose, want) {
			t.Errorf("docker-compose.yml missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(compose), "dapr") {
		t.Fatal("docker-compose.yml mentions dapr")
	}

	mkBytes, err := os.ReadFile(filepath.Join(dir, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mkBytes), "docker compose up") {
		t.Fatal("Makefile missing infra target")
	}

	svc := filepath.Join(dir, "services", "orders")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := renderTemplate("templates/service", svc, scaffoldData{
		AppName: "orders",
		Module:  "github.com/me/platform",
	}); err != nil {
		t.Fatal(err)
	}

	mustExist(t, svc, "cmd/main.go", "internal/hello/hello.go", "configs/config.yaml", "README.md")
	mustNotExist(t, svc, "dapr.yaml", "go.mod", "configs/dapr")

	mainBytes, err := os.ReadFile(filepath.Join(svc, "cmd/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	main := string(mainBytes)
	for _, want := range []string{
		`github.com/me/platform/services/orders/internal/hello`,
		`fxkit.Default()`,
		`fxhuma.Module`,
		`cli.SetRootName("orders"`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.go missing %q\n%s", want, main)
		}
	}
	if strings.Contains(strings.ToLower(main), "dapr") {
		t.Fatal("main.go mentions dapr")
	}

	cfgBytes, err := os.ReadFile(filepath.Join(svc, "configs/config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(cfgBytes)
	for _, want := range []string{"hatchet:", "outbox:", "auth:", "otel:", "discovery:", `name: "orders"`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.yaml missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(cfg), "dapr") {
		t.Fatal("config.yaml mentions dapr")
	}

	helloBytes, err := os.ReadFile(filepath.Join(svc, "internal/hello/hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	hello := string(helloBytes)
	if !strings.Contains(hello, "fxhuma.ProvideRegistrar") {
		t.Fatal("hello.go should register via Huma")
	}
	if strings.Contains(hello, "chi.URLParam") {
		t.Fatal("hello.go should not use raw chi routing")
	}
}

func TestScaffoldMinimalService(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := renderTemplate("templates/service", dir, scaffoldData{
		AppName: "tiny",
		Module:  "github.com/me/platform",
		Minimal: true,
	}); err != nil {
		t.Fatal(err)
	}

	mainBytes, err := os.ReadFile(filepath.Join(dir, "cmd/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	main := string(mainBytes)
	if !strings.Contains(main, "fxkit.Minimal()") {
		t.Fatalf("expected Minimal bundle:\n%s", main)
	}
	if !strings.Contains(main, "fxhuma.Module") {
		t.Fatal("minimal service should still mount Huma")
	}

	cfgBytes, err := os.ReadFile(filepath.Join(dir, "configs/config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(cfgBytes)
	if strings.Contains(cfg, "hatchet:") || strings.Contains(cfg, "outbox:") {
		t.Fatalf("minimal config should omit default-stack sections:\n%s", cfg)
	}
	if !strings.Contains(cfg, `name: "tiny"`) {
		t.Fatalf("missing app name:\n%s", cfg)
	}
}

func mustExist(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(root, rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func mustNotExist(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(root, rel)
		if _, err := os.Stat(p); err == nil {
			t.Errorf("unexpected path %s", rel)
		}
	}
}
