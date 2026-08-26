package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestParseResourceArgs_listSpec(t *testing.T) {
	t.Parallel()
	data, err := parseResourceArgs(
		"Article",
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
		seen[s]++
		if seen[s] > 1 {
			t.Fatalf("duplicate SortFields %q", s)
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
		"Article",
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
		`Kind: crudx.FieldTime`,
		`Column: "articles.author_id"`,
		`Kind: crudx.FieldNumber`,
		`Select("title", "body", "author_id")`,
		"crudx.ListResult[ArticleListRow]",
		`gorm:"size:255;not null;index"`,
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
	}
	for _, s := range mustNot {
		if bytes.Contains(src, []byte(s)) {
			t.Errorf("generated source should not contain %q", s)
		}
	}
	if n := strings.Count(string(src), `"created_at":`); n != 2 {
		// one in Fields, one in SortFields
		t.Errorf("created_at map keys=%d want 2 (fields+sort)\n%s", n, src)
	}
}
