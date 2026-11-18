package authz_test

import (
	"testing"

	"github.com/fitan/fxkit/authz"
)

func TestHumaPathToCasbinEscapesRegexMetacharacters(t *testing.T) {
	cases := map[string]string{
		"/users/{id}":        "/users/:id",
		"/files/{name}.json": `/files/:name\.json`,
		"/a(b)":              `/a\(b\)`,
		"/x+y":               `/x\+y`,
	}
	for in, want := range cases {
		if got := authz.HumaPathToCasbin(in); got != want {
			t.Errorf("HumaPathToCasbin(%q)=%q, want %q", in, got, want)
		}
	}
}
