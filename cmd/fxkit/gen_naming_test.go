package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// TestGormNamingDerivation 是回归测试：表名/列名必须与 GORM 一致。
// 自造的 toSnake/plural 曾把 Person 变成 persons（GORM: people）、
// APIKey 变成 a_p_i_key（GORM: api_key），生成的代码编译通过但运行期 SQL 报错。
func TestGormNamingDerivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		table string
	}{
		{"Person", "people"},
		{"APIKey", "api_keys"},
		{"URL", "urls"},
		{"UserID", "user_ids"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			data, err := parseResourceArgs(c.name, "", []string{"name:string"}, nil, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if data.Table != c.table {
				t.Errorf("Table = %q, want %q", data.Table, c.table)
			}
			if data.Path != "/"+c.table {
				t.Errorf("Path = %q, want %q", data.Path, "/"+c.table)
			}
			if data.Package != c.table {
				t.Errorf("Package = %q, want %q", data.Package, c.table)
			}
		})
	}
}

// TestGormNamingColumns 校验列名同样来自 GORM 的 NamingStrategy。
func TestGormNamingColumns(t *testing.T) {
	t.Parallel()
	data, err := parseResourceArgs("Person", "",
		[]string{"UserID:int64", "APIKey:string", "created_by:string"},
		nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"UserID": "user_id", "APIKey": "api_key", "CreatedBy": "created_by"}
	for _, f := range data.Fields {
		if got := want[f.Name]; got != f.Column {
			t.Errorf("field %s column = %q, want %q", f.Name, f.Column, got)
		}
	}

	src, err := renderResource(data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, s := range []string{
		`Model(&Person{}).Table("people")`,
		`Select("user_id", "api_key", "created_by")`,
	} {
		if !bytes.Contains(src, []byte(s)) {
			t.Errorf("generated source missing %q", s)
		}
	}
	// gofmt 会按列对齐 struct 字段，这里用正则匹配。
	if !regexp.MustCompile(`APIKey\s+string`).Match(src) {
		t.Errorf("generated source missing the APIKey field:\n%s", src)
	}
	if bytes.Contains(src, []byte("persons")) {
		t.Error("generated source must not use the hand-rolled plural form")
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "person.go", src, parser.AllErrors); err != nil {
		t.Fatalf("parse generated source: %v\n%s", err, src)
	}
}

// TestResourceFieldValidation 覆盖字段名规则与内置字段冲突。
func TestResourceFieldValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		desc   string
		fields []string
	}{
		{"created_at collides with the generated CreatedAt", []string{"created_at:time"}},
		{"createdAt is the same field", []string{"createdAt:time"}},
		{"updated_at collides", []string{"updated_at:time"}},
		{"id collides with the primary key", []string{"id:int64"}},
		{"exact duplicate", []string{"title:string", "title:string"}},
		{"case-insensitive duplicate", []string{"title:string", "Title:string"}},
		{"same Go field name", []string{"user_id:int64", "userId:int64"}},
		{"same json name", []string{"a_b:string", "aB:string"}},
		{"path separators are not identifiers", []string{"../evil:string"}},
		{"quotes are not identifiers", []string{`ti"tle:string`}},
		{"leading digit", []string{"1title:string"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.desc, func(t *testing.T) {
			t.Parallel()
			if _, err := parseResourceArgs("Article", "", c.fields, nil, nil, ""); err == nil {
				t.Fatalf("expected error for fields %v", c.fields)
			}
		})
	}
}

// TestResourceColumnReferences 校验 --sort/--search/--filter 引用的列必须存在。
func TestResourceColumnReferences(t *testing.T) {
	t.Parallel()
	fields := []string{"title:string", "author_id:int64"}

	if _, err := parseResourceArgs("Article", "", fields, []string{"nope"}, nil, ""); err == nil {
		t.Error("--search with an unknown column should fail")
	}
	if _, err := parseResourceArgs("Article", "", fields, nil, []string{"nope"}, ""); err == nil {
		t.Error("--filter with an unknown column should fail")
	}
	if _, err := parseResourceArgs("Article", "", fields, nil, nil, "nope desc"); err == nil {
		t.Error("--sort with an unknown column should fail")
	}
	// 注入尝试也应该在列校验阶段被拒绝。
	if _, err := parseResourceArgs("Article", "", fields, nil, nil, `title"; os.Exit(1); //`); err == nil {
		t.Error("--sort must not pass through a Go-literal injection")
	}

	// 合法列（含 Go 字段名 / JSON 名 / 内置列）应被归一为真实列名。
	data, err := parseResourceArgs("Article", "", fields,
		[]string{"Title"}, []string{"authorId", "created_at"}, "createdAt")
	if err != nil {
		t.Fatal(err)
	}
	if len(data.SearchOn) != 1 || data.SearchOn[0] != "title" {
		t.Errorf("SearchOn = %v, want [title]", data.SearchOn)
	}
	if strings.Join(data.FilterOn, ",") != "author_id,created_at" {
		t.Errorf("FilterOn = %v", data.FilterOn)
	}
	if data.SortBy != "created_at asc" {
		t.Errorf("SortBy = %q, want %q", data.SortBy, "created_at asc")
	}
}

// TestResourceTableOverrideAndInjection 校验 --table 的校验与转义。
func TestResourceTableOverrideAndInjection(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{`users"; DROP TABLE x; --`, "users;drop", "user table", "../users", ""} {
		if bad == "" {
			continue
		}
		if _, err := parseResourceArgs("Article", bad, []string{"title:string"}, nil, nil, ""); err == nil {
			t.Errorf("expected --table %q to be rejected", bad)
		}
	}

	data, err := parseResourceArgs("Article", "blog_posts", []string{"title:string"}, []string{"title"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if data.Table != "blog_posts" || data.Package != "blog_posts" {
		t.Fatalf("table/package = %q/%q", data.Table, data.Package)
	}
	src, err := renderResource(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`Table("blog_posts")`,
		`Column: "blog_posts.title"`,
		`DefaultSort: "created_at desc"`,
	} {
		if !bytes.Contains(src, []byte(want)) {
			t.Errorf("generated source missing %q", want)
		}
	}
	// --table 必须对 GORM 的所有操作都生效，而不只是 crudx.List。
	if !bytes.Contains(src, []byte(`func (Article) TableName() string { return "blog_posts" }`)) {
		t.Errorf("generated model must pin TableName:\n%s", src)
	}
}

// TestResourceRejectsUnsafeName 校验 `fxkit gen resource ../pwn` 不会在上层建目录。
func TestResourceRejectsUnsafeName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"../pwn", "a/b", "..", "", "a b"} {
		if _, err := parseResourceArgs(name, "", []string{"title:string"}, nil, nil, ""); err == nil {
			t.Errorf("expected resource name %q to be rejected", name)
		}
	}
	// 没有字段时会生成未使用变量的代码，直接拒绝。
	if _, err := parseResourceArgs("Article", "", nil, nil, nil, ""); err == nil {
		t.Error("expected at least one --field to be required")
	}
}

// TestGeneratedSourceQuoting 校验列名等值插入源码时都经 strconv.Quote。
func TestGeneratedSourceQuoting(t *testing.T) {
	t.Parallel()
	data, err := parseResourceArgs("Article", "article", []string{"title:string"}, []string{"title"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	src, err := renderResource(data)
	if err != nil {
		t.Fatal(err)
	}
	// 关键字/包名一律以带引号的字面量出现
	for _, want := range []string{
		`fxkit.Service("article",`,
		`fxerrors.Internal("article: nil gormx client")`,
		`Tags: []string{"Article"}`,
		`Path: "/article"`,
	} {
		if !bytes.Contains(src, []byte(want)) {
			t.Errorf("generated source missing %q", want)
		}
	}
}

// TestRelativeToModule 校验输出目录必须落在当前 module 内。
func TestRelativeToModule(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	inner := root + "/internal/api"
	if rel, err := relativeToModule(root, inner); err != nil || rel != "internal/api" {
		t.Fatalf("rel=%q err=%v", rel, err)
	}
	if _, err := relativeToModule(root, root+"/../pwn"); err == nil {
		t.Fatal("expected out-of-module path to be rejected")
	}
	if _, err := relativeToModule(root, root); err == nil {
		t.Fatal("expected module root itself to be rejected")
	}
}
