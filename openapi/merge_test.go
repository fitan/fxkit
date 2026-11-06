package openapi

import (
	"strings"
	"testing"
)

func TestMergeYAML_pathsAndSchemas(t *testing.T) {
	base := []byte(`
openapi: 3.1.0
info:
  title: Base
paths:
  /v1/hello:
    get:
      operationId: Hello
components:
  schemas:
    HelloRequest:
      type: object
`)
	overlay := []byte(`
openapi: 3.1.0
info:
  title: Huma
paths:
  /demo/fail:
    get:
      operationId: demo-fail
      tags: [Demo]
components:
  schemas:
    TriggerFailInput:
      type: object
tags:
  - name: Demo
`)
	out, err := MergeYAML(base, overlay)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"/v1/hello", "/demo/fail", "HelloRequest", "TriggerFailInput", "Demo"} {
		if !strings.Contains(s, want) {
			t.Fatalf("merged spec missing %q:\n%s", want, s)
		}
	}
}

func TestMergeBaseHuma_emptyHuma(t *testing.T) {
	base := []byte(`openapi: 3.1.0
paths: {}
`)
	out, err := MergeBaseHuma(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(base) {
		t.Fatalf("expected base unchanged, got %s", out)
	}
}

// TestMergeYAML_overlayInfoWins 是回归测试：
// mergeInfo 曾经「只填空不覆盖」，而 docs 用内嵌占位骨架作 base，
// 于是 /openapi.yaml 的 info.title 永远是占位值，客户端 codegen 拿到错误服务名。
func TestMergeYAML_overlayInfoWins(t *testing.T) {
	base := []byte("openapi: 3.1.0\ninfo:\n  title: fxkit service\n  version: 0.0.0\npaths: {}\n")
	overlay := []byte("openapi: 3.1.0\ninfo:\n  title: Catalog API\n  version: 1.2.3\npaths: {}\n")

	out, err := MergeYAML(base, overlay)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "Catalog API") || !strings.Contains(s, "1.2.3") {
		t.Fatalf("overlay info must win:\n%s", s)
	}
	if strings.Contains(s, "fxkit service") {
		t.Fatalf("placeholder info leaked into merged spec:\n%s", s)
	}
}

// TestMergeYAML_keepsExtraTopLevelKeys 是回归测试：
// 早期只合并 info/paths/components/tags/servers，overlay 里的 security
// （以及 webhooks / externalDocs 等）会被静默丢弃。
func TestMergeYAML_keepsExtraTopLevelKeys(t *testing.T) {
	base := []byte("openapi: 3.1.0\ninfo:\n  title: base\npaths: {}\n")
	overlay := []byte("openapi: 3.1.0\nsecurity:\n  - bearerAuth: []\nwebhooks:\n  ping:\n    post: {}\n")

	out, err := MergeYAML(base, overlay)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "security") || !strings.Contains(s, "bearerAuth") {
		t.Fatalf("security must survive merge:\n%s", s)
	}
	if !strings.Contains(s, "webhooks") {
		t.Fatalf("webhooks must survive merge:\n%s", s)
	}
}
