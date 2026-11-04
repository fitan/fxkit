package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseResourceArgs_listSpec(t *testing.T) {
	t.Parallel()
	data, err := parseResourceArgs(
		"Article", "",
		[]string{"title:string:required", "body:text", "author_id:int64:index", "published_at:time:optional"},
		[]string{"title", "body"},
		[]string{"author_id", "created_at"},
		"created_at desc",
	)
	if err != nil {
		t.Fatal(err)
	}

	kind := map[string]string{}
	indexed := map[string]bool{}
	for _, f := range data.ListFields {
		if _, dup := kind[f.Name]; dup {
			t.Fatalf("duplicate ListFields key %q", f.Name)
		}
		kind[f.Name] = f.Kind
		indexed[f.Name] = f.Indexed
	}
	if kind["created_at"] != "crudx.FieldTime" {
		t.Fatalf("created_at kind=%s", kind["created_at"])
	}
	if kind["author_id"] != "crudx.FieldNumber" {
		t.Fatalf("author_id kind=%s", kind["author_id"])
	}
	if kind["published_at"] != "" {
		t.Fatalf("published_at should not be in spec unless search/filter: %v", kind)
	}
	if !indexed["title"] || !indexed["body"] {
		t.Fatalf("search fields must be indexed: %v", indexed)
	}
	var titleGorm string
	for _, f := range data.Fields {
		if f.Column == "title" {
			titleGorm = f.GormTag
		}
	}
	if !strings.Contains(titleGorm, "index") {
		t.Fatalf("search column title must get a GORM index, gorm=%q", titleGorm)
	}
	if !indexed["author_id"] {
		t.Fatal("author_id has index flag, want Indexed")
	}

	seen := map[string]int{}
	for _, s := range data.SortFields {
		seen[s.Name]++
		if seen[s.Name] > 1 {
			t.Fatalf("duplicate SortFields %q", s.Name)
		}
	}
	for _, want := range []string{"id", "created_at", "updated_at", "title", "body", "author_id"} {
		if seen[want] != 1 {
			t.Fatalf("SortFields missing %q: %v", want, data.SortFields)
		}
	}
}

func TestRenderResource_listEngine(t *testing.T) {
	t.Parallel()
	data, err := parseResourceArgs(
		"Article", "",
		[]string{"title:string:required", "body:text", "author_id:int64:index"},
		[]string{"title", "body"},
		[]string{"author_id"},
		"created_at desc",
	)
	if err != nil {
		t.Fatal(err)
	}
	src, err := renderResource(data)
	if err != nil {
		t.Fatalf("render: %v\n%s", err, src)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "article.go", src, parser.AllErrors); err != nil {
		t.Fatalf("parse generated source: %v\n%s", err, src)
	}

	mustContain := []string{
		"crudx.List(",
		"fxhuma.RegisterResource(",
		// ListSpec 用 map 字面量声明（每行自包含，无隐式链式状态）
		"crudx.ListSpec{",
		"Fields: map[string]crudx.FieldSpec{",
		`"author_id":`, "Column: \"articles.author_id\", Kind: crudx.FieldNumber",
		`"title":`, "Column: \"articles.title\", Kind: crudx.FieldString",
		"SortFields: map[string]string{",
		"DefaultSort:",
		`Select("title", "body", "author_id")`,
		"crudx.ListResult[ArticleListRow]",
		`gorm:"size:255;not null;index"`,
		"s.client.WithTxResult",
	}
	for _, s := range mustContain {
		if !bytes.Contains(src, []byte(s)) {
			t.Errorf("generated source missing %q", s)
		}
	}
	mustNot := []string{
		"crudx.ApplyList(",
		"crudx.NewRepo",
		"server.ProvideRoutes",
		"type ListArticleResp",
		// 已废弃的 builder API 不应再出现在生成代码里
		"crudx.NewSpec[",
		"SpecBuilder",
		// 生成代码必须使用项目自身的 DI 抽象；
		// 早期模板误用 uber-go/fx，导致 `fxkit gen resource` 产物无法编译。
		"fx.Module(",
		"fx.Provide(",
		"var Module = fx.",
	}
	for _, s := range mustNot {
		if bytes.Contains(src, []byte(s)) {
			t.Errorf("generated source should not contain %q", s)
		}
	}
	// created_at 应出现在 Fields 和 SortFields 两处（map 写法各声明一次）
	if n := strings.Count(string(src), `"created_at"`); n < 2 {
		t.Errorf("created_at should be declared in both Fields and SortFields, got %d\n%s", n, src)
	}
}

// TestRenderResource_likeableOnlyForSearch 是回归测试：
// 早期模板用 Indexed 判断是否生成模糊查询，导致 `--filter author_id`（数值列）
// 也被允许 ~ 字符串匹配。现在 Likeable 只对 --search 列生成。
func TestRenderResource_likeableOnlyForSearch(t *testing.T) {
	t.Parallel()
	data, err := parseResourceArgs(
		"Article", "",
		[]string{"title:string:required", "author_id:int64:index"},
		[]string{"title"},     // 只有 title 可模糊搜索
		[]string{"author_id"}, // author_id 仅等值过滤
		"created_at desc",
	)
	if err != nil {
		t.Fatal(err)
	}
	src, err := renderResource(data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	out := string(src)

	// 只在 Fields 块内查找（SortFields 里也有 "title": 这样的键）
	fieldsStart := strings.Index(out, "Fields: map[string]crudx.FieldSpec{")
	fieldsEnd := strings.Index(out, "SortFields: map[string]string{")
	if fieldsStart < 0 || fieldsEnd < 0 || fieldsEnd < fieldsStart {
		t.Fatalf("cannot locate Fields/SortFields blocks:\n%s", out)
	}
	fieldsBlock := out[fieldsStart:fieldsEnd]

	var titleLine, authorLine string
	for _, line := range strings.Split(fieldsBlock, "\n") {
		switch {
		case strings.Contains(line, `"title":`):
			titleLine = line
		case strings.Contains(line, `"author_id":`):
			authorLine = line
		}
	}
	if titleLine == "" || authorLine == "" {
		t.Fatalf("expected both title and author_id in Fields:\n%s", out)
	}
	if !strings.Contains(titleLine, "Likeable: true") {
		t.Errorf("title comes from --search and should be Likeable: %s", titleLine)
	}
	if strings.Contains(authorLine, "Likeable: true") {
		t.Errorf("author_id is a numeric filter-only column and must NOT be Likeable: %s", authorLine)
	}
}

// TestRenderResource_moduleUsesProjectDI 是回归测试：
// 生成模板曾写成 `fx.Module(...)`，但文件里导入的是 di 而不是 uber-go/fx，
// 导致 `fxkit gen resource` 的产物无法编译（只在纯语法层检查时不报错）。
func TestRenderResource_moduleUsesProjectDI(t *testing.T) {
	t.Parallel()
	data, err := parseResourceArgs(
		"Article", "",
		[]string{"title:string:required"},
		nil, nil, "created_at desc",
	)
	if err != nil {
		t.Fatal(err)
	}
	src, err := renderResource(data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// 1. 不得引用项目未依赖的 fx
	if bytes.Contains(src, []byte("fx.Module(")) || bytes.Contains(src, []byte("fx.Provide(")) {
		t.Fatalf("generated module must not use uber-go/fx:\n%s", src)
	}

	// 2. 必须使用 di.Options / fxkit.Service
	for _, want := range []string{`di.Options(`, `fxkit.Service(`} {
		if !bytes.Contains(src, []byte(want)) {
			t.Errorf("generated module missing %q:\n%s", want, src)
		}
	}

	// 3. 文件里出现的所有包别名都必须真的被 import（防止再出现 fx 这类悬空标识符）
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "article.go", src, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	imported := map[string]bool{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		// 别名优先级：显式别名 > 路径最后一段
		alias := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		imported[alias] = true
		// v2 这类版本后缀不是合法别名，包名通常是倒数第二段（如 huma/v2 -> huma）
		segs := strings.Split(path, "/")
		for i := len(segs) - 1; i >= 0; i-- {
			seg := segs[i]
			if seg == "" || (len(seg) > 1 && seg[0] == 'v' && isAllDigits(seg[1:])) {
				continue
			}
			imported[seg] = true
			break
		}
	}
	used := map[string]bool{}
	for _, ident := range packageAliases(file) {
		used[ident] = true
	}
	for alias := range used {
		if !imported[alias] {
			t.Errorf("generated source references undefined package alias %q", alias)
		}
	}
}

// isAllDigits 报告 s 是否全为数字（用于识别 /v2 这类版本后缀）。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// packageAliases 收集「未被声明为局部变量/参数」的小写限定符，
// 也就是形如 `pkg.Ident` 里真正的包别名。
func packageAliases(file *ast.File) []string {
	// 先收集所有已声明的标识符（变量、参数、接收者等）。
	declared := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.ValueSpec:
			for _, name := range v.Names {
				declared[name.Name] = true
			}
		case *ast.AssignStmt:
			if v.Tok == token.DEFINE {
				for _, lhs := range v.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						declared[id.Name] = true
					}
				}
			}
		case *ast.Field:
			for _, name := range v.Names {
				declared[name.Name] = true
			}
		case *ast.RangeStmt:
			for _, e := range []ast.Expr{v.Key, v.Value} {
				if id, ok := e.(*ast.Ident); ok {
					declared[id.Name] = true
				}
			}
		}
		return true
	})

	seen := map[string]bool{}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || !ast.IsExported(sel.Sel.Name) || declared[id.Name] {
			return true
		}
		if !seen[id.Name] {
			seen[id.Name] = true
			out = append(out, id.Name)
		}
		return true
	})
	return out
}

func TestGenerateClient_scaffold(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	spec := []byte("openapi: 3.0.3\ninfo:\n  title: t\n  version: 1.0.0\npaths: {}\n")
	specPath := filepath.Join(dir, "in.yaml")
	if err := os.WriteFile(specPath, spec, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "users")
	if err := generateClient(clientGenInput{
		SpecPath:    specPath,
		OutDir:      out,
		SkipCodegen: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"openapi.yaml", "oapi-codegen.yaml", "generate.go", "reqx.go"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	reqxSrc, err := os.ReadFile(filepath.Join(out, "reqx.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package users",
		"NewFromFactory",
		"ProvideClient",
		"TransportClient",
		"*ClientWithResponses",
	} {
		if !bytes.Contains(reqxSrc, []byte(want)) {
			t.Errorf("reqx.go missing %q\n%s", want, reqxSrc)
		}
	}
	genSrc, err := os.ReadFile(filepath.Join(out, "generate.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(genSrc, []byte(oapiCodegenPkg)) {
		t.Errorf("generate.go should pin %s\n%s", oapiCodegenPkg, genSrc)
	}
}

func TestValidateGoPackageName(t *testing.T) {
	t.Parallel()
	if err := validateGoPackageName("users"); err != nil {
		t.Fatal(err)
	}
	if err := validateGoPackageName("type"); err == nil {
		t.Fatal("keyword")
	}
	if err := validateGoPackageName("123bad"); err == nil {
		t.Fatal("leading digit")
	}
}

func TestToCamel_acronyms(t *testing.T) {
	cases := map[string]string{
		"Title":  "title",
		"UserID": "userID",
		"APIKey": "apiKey",
		"URL":    "url",
	}
	for in, want := range cases {
		if got := toCamel(in); got != want {
			t.Errorf("toCamel(%q) = %q, want %q", in, got, want)
		}
	}
}
