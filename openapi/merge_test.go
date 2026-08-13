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
