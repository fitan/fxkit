package main

import (
	"errors"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

// oapiCodegenPkg is the generator `go run` target. Pinned so `fxkit gen client`
// and the written //go:generate line stay in sync.
const oapiCodegenPkg = "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0"

// clientGeneratedFiles 是每次都能安全重写的纯生成物。
var clientGeneratedFiles = []string{"openapi.yaml", "oapi-codegen.yaml", "client.gen.go"}

// clientUserFiles 是用户很可能手改过的文件：默认保留，--force 才覆盖。
var clientUserFiles = []string{"generate.go", "reqx.go"}

// runCodegen 是 oapi-codegen 的调用入口，测试中可替换。
var runCodegen = runOapiCodegen

func genClientCmd() *cobra.Command {
	var (
		specPath    string
		outDir      string
		pkg         string
		skipCodegen bool
		force       bool
	)
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Generate a Go OpenAPI client (oapi-codegen) wired to reqx",
		Long: `Generate a typed Go SDK from an OpenAPI spec (typically dumped with
` + "`<svc> openapi`" + `) using oapi-codegen, plus a reqx helper so the client
uses Consul watch / failover / OTel instead of a hardcoded base URL.

  myapp openapi -o /tmp/users.openapi.yaml
  fxkit gen client --spec /tmp/users.openapi.yaml --out ./internal/clients/users

Files are generated into a staging directory first and only moved into --out
after oapi-codegen succeeds, so a failed run never leaves a half-written package.

Existing reqx.go / generate.go are kept (you may have edited them); pass --force
to overwrite. openapi.yaml / oapi-codegen.yaml / client.gen.go are always refreshed.

Requires ` + "`go`" + ` on PATH to run oapi-codegen (unless --skip-codegen).
The generated NewFromFactory takes reqx.ClientInput — set Name to the Consul
service name (or Seeds for local).
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return generateClient(clientGenInput{
				SpecPath:    specPath,
				OutDir:      outDir,
				Package:     pkg,
				SkipCodegen: skipCodegen,
				Force:       force,
			})
		},
	}
	cmd.Flags().StringVar(&specPath, "spec", "", "OpenAPI YAML/JSON file (from <svc> openapi)")
	cmd.Flags().StringVar(&outDir, "out", "", "output package directory")
	cmd.Flags().StringVar(&pkg, "package", "", "Go package name (default: basename of --out)")
	cmd.Flags().BoolVar(&skipCodegen, "skip-codegen", false, "write scaffold only; do not run oapi-codegen")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing reqx.go / generate.go")
	_ = cmd.MarkFlagRequired("spec")
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

type clientGenInput struct {
	SpecPath    string
	OutDir      string
	Package     string
	SkipCodegen bool
	Force       bool
}

func generateClient(in clientGenInput) error {
	specPath := strings.TrimSpace(in.SpecPath)
	outDir := strings.TrimSpace(in.OutDir)
	if specPath == "" {
		return fmt.Errorf("gen client: --spec is required")
	}
	if outDir == "" {
		return fmt.Errorf("gen client: --out is required")
	}

	spec, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("gen client spec: %w", err)
	}
	if len(strings.TrimSpace(string(spec))) == 0 {
		return fmt.Errorf("gen client: spec %s is empty", specPath)
	}

	pkg := strings.TrimSpace(in.Package)
	if pkg == "" {
		pkg = filepath.Base(filepath.Clean(outDir))
	}
	if err := validateGoPackageName(pkg); err != nil {
		return fmt.Errorf("gen client: %w", err)
	}

	// 1. 在 --out 的兄弟目录里搭舞台：go run 能复用模块缓存，
	//    失败时整个目录删掉，不会污染用户工程（. 开头也会被 go 工具忽略）。
	parent := filepath.Dir(filepath.Clean(outDir))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("gen client mkdir: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".fxkit-client-*")
	if err != nil {
		return fmt.Errorf("gen client staging dir: %w", err)
	}
	defer os.RemoveAll(stage)

	if err := writeClientScaffold(stage, pkg, spec); err != nil {
		return err
	}
	if !in.SkipCodegen {
		if err := runCodegen(stage); err != nil {
			return err
		}
	}

	// 2. codegen 成功后才动 outDir，且每个文件都用原子写。
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("gen client mkdir: %w", err)
	}
	return installClientFiles(stage, outDir, in.Force)
}

// installClientFiles 把舞台目录里的产物搬进 outDir。
// 生成物总是刷新；用户可能手改的文件默认保留。
func installClientFiles(stage, outDir string, force bool) error {
	for _, name := range clientGeneratedFiles {
		src := filepath.Join(stage, name)
		if _, err := os.Stat(src); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // --skip-codegen 时没有 client.gen.go
			}
			return err
		}
		dst := filepath.Join(outDir, name)
		if _, err := os.Stat(dst); err == nil {
			fmt.Printf("overwriting %s\n", dst)
		}
		if err := copyFileAtomic(src, dst); err != nil {
			return err
		}
	}

	for _, name := range clientUserFiles {
		src := filepath.Join(stage, name)
		if _, err := os.Stat(src); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		dst := filepath.Join(outDir, name)
		if _, err := os.Stat(dst); err == nil && !force {
			fmt.Printf("keeping existing %s (use --force to overwrite)\n", dst)
			continue
		}
		if err := copyFileAtomic(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFileAtomic(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	return writeFileAtomic(dst, data, 0o644)
}

func writeClientScaffold(outDir, pkg string, spec []byte) error {
	if err := writeFileAtomic(filepath.Join(outDir, "openapi.yaml"), spec, 0o644); err != nil {
		return fmt.Errorf("gen client write openapi.yaml: %w", err)
	}

	cfg := fmt.Sprintf(`# yaml-language-server: $schema=https://raw.githubusercontent.com/oapi-codegen/oapi-codegen/v2.8.0/configuration-schema.json
package: %s
output: client.gen.go
generate:
  models: true
  client: true
`, pkg)
	if err := writeFileAtomic(filepath.Join(outDir, "oapi-codegen.yaml"), []byte(cfg), 0o644); err != nil {
		return fmt.Errorf("gen client write oapi-codegen.yaml: %w", err)
	}

	genGo := fmt.Sprintf("//go:generate go run %s -config oapi-codegen.yaml openapi.yaml\n\npackage %s\n", oapiCodegenPkg, pkg)
	if err := writeFileAtomic(filepath.Join(outDir, "generate.go"), []byte(genGo), 0o644); err != nil {
		return fmt.Errorf("gen client write generate.go: %w", err)
	}

	wrapper, err := renderClientReqx(pkg)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(outDir, "reqx.go"), wrapper, 0o644); err != nil {
		return fmt.Errorf("gen client write reqx.go: %w", err)
	}
	return nil
}

func renderClientReqx(pkg string) ([]byte, error) {
	src := fmt.Sprintf(`package %s

import (
	"fmt"

	"github.com/fitan/fxkit/reqx"
)

// NewFromFactory builds the generated OpenAPI client on a [reqx] transport
// (Consul watch, failover, OTel). Set ClientInput.Name to the Consul service
// name; use Seeds for local/dev without Consul. Do not hardcode instance IPs.
func NewFromFactory(f *reqx.Factory, in reqx.ClientInput) (*ClientWithResponses, error) {
	if f == nil {
		return nil, fmt.Errorf("%s: nil reqx factory")
	}
	httpClient, server, err := f.TransportClient(in)
	if err != nil {
		return nil, err
	}
	return NewClientWithResponses(server, WithHTTPClient(httpClient))
}

// ProvideClient returns an fx constructor function that builds and injects
// *ClientWithResponses through [reqx.Factory] and the given [reqx.ClientInput].
//
// Usage:
//   fx.Provide(%s.ProvideClient(reqx.ClientInput{Name: "%s"}))
func ProvideClient(in reqx.ClientInput) any {
	return func(f *reqx.Factory) (*ClientWithResponses, error) {
		return NewFromFactory(f, in)
	}
}
`, pkg, pkg, pkg, pkg)
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("gofmt reqx.go: %w", err)
	}
	return formatted, nil
}

func runOapiCodegen(dir string) error {
	cmd := exec.Command("go", "run", oapiCodegenPkg, "-config", "oapi-codegen.yaml", "openapi.yaml")
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gen client: oapi-codegen: %w (install Go and allow GOPROXY, or pass --skip-codegen)", err)
	}
	return nil
}

func validateGoPackageName(name string) error {
	if name == "" {
		return fmt.Errorf("package name is empty")
	}
	if goKeywords[name] {
		return fmt.Errorf("package name %q is a Go keyword", name)
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return fmt.Errorf("package name %q must start with a letter", name)
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return fmt.Errorf("package name %q is not a valid Go identifier", name)
		}
	}
	return nil
}

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}
