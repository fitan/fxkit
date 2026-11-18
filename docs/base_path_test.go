package docs

import (
	"strings"
	"testing"
)

func TestMergedRoutesWithBasePath(t *testing.T) {
	routes := MergedRoutesWithOptions(nil, nil, RouteOptions{BasePath: "/api/"})
	got := map[string]bool{}
	for _, r := range routes {
		got[r.Pattern] = true
	}
	for _, want := range []string{"/api/docs", "/api/docs/", "/api/openapi.yaml", "/api/openapi.json"} {
		if !got[want] {
			t.Errorf("missing route %q (got %v)", want, got)
		}
	}
}

func TestScalarHTMLBasePathAndSRI(t *testing.T) {
	page := scalarHTML(RouteOptions{
		BasePath:  "/api",
		ScalarURL: "https://cdn.example/scalar.js",
		ScalarSRI: "sha384-abc",
	})
	if !strings.Contains(page, `data-url="/api/openapi.yaml"`) {
		t.Fatalf("spec URL not prefixed: %s", page)
	}
	if !strings.Contains(page, `src="https://cdn.example/scalar.js"`) {
		t.Fatalf("scalar URL not used: %s", page)
	}
	if !strings.Contains(page, `integrity="sha384-abc"`) {
		t.Fatalf("SRI missing: %s", page)
	}
}

func TestNormalizeBasePath(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"/":     "",
		"api":   "/api",
		"/api":  "/api",
		"/api/": "/api",
	}
	for in, want := range cases {
		if got := normalizeBasePath(in); got != want {
			t.Errorf("normalizeBasePath(%q)=%q, want %q", in, got, want)
		}
	}
}
