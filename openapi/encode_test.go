package openapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/go-chi/chi/v5"
)

func TestParseSpecVersionAndFormat(t *testing.T) {
	t.Parallel()
	v, err := ParseSpecVersion("3.0.3")
	if err != nil || v != Spec30 {
		t.Fatalf("3.0.3: %q %v", v, err)
	}
	v, err = ParseSpecVersion("3.1")
	if err != nil || v != Spec31 {
		t.Fatalf("3.1: %q %v", v, err)
	}
	if _, err := ParseSpecVersion("2.0"); err == nil {
		t.Fatal("expected error for 2.0")
	}
	f, err := ParseFormat("yml")
	if err != nil || f != FormatYAML {
		t.Fatalf("yml: %q %v", f, err)
	}
	f, err = ParseFormat("json")
	if err != nil || f != FormatJSON {
		t.Fatalf("json: %q %v", f, err)
	}
}

func TestEncode_downgrade30(t *testing.T) {
	t.Parallel()
	api := testGreetingAPI(t)
	b, err := Encode(api, EncodeInput{Version: Spec30, Format: FormatYAML})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "3.0.3") {
		t.Fatalf("want OpenAPI 3.0.3:\n%s", s)
	}
	if !strings.Contains(s, "/greeting/{name}") {
		t.Fatalf("missing path:\n%s", s)
	}
	if !strings.Contains(s, "get-greeting") {
		t.Fatalf("missing operationId:\n%s", s)
	}

	js, err := Encode(api, EncodeInput{Version: Spec31, Format: FormatJSON})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `"openapi": "3.1`) {
		t.Fatalf("want 3.1 json:\n%s", js)
	}
}

func TestEncode_mergeBase(t *testing.T) {
	t.Parallel()
	api := testGreetingAPI(t)
	base := []byte(`
openapi: 3.1.0
info:
  title: Base
paths:
  /healthz:
    get:
      operationId: healthz
`)
	b, err := Encode(api, EncodeInput{Version: Spec30, Format: FormatYAML, Base: base})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "3.0.3") {
		t.Fatalf("merged should stay 3.0:\n%s", s)
	}
	if !strings.Contains(s, "/healthz") || !strings.Contains(s, "/greeting/{name}") {
		t.Fatalf("merged paths:\n%s", s)
	}
}

func TestEncode_nilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Encode(nil, EncodeInput{}); err == nil {
		t.Fatal("expected error")
	}
}

func testGreetingAPI(t *testing.T) huma.API {
	t.Helper()
	mux := chi.NewRouter()
	api := fxhuma.NewAPI(mux, &config.App{Name: "greet-test"})
	fxhuma.Register(api, huma.Operation{
		OperationID: "get-greeting",
		Method:      http.MethodGet,
		Path:        "/greeting/{name}",
		Summary:     "Get a greeting",
	}, func(_ context.Context, in *struct {
		Name string `path:"name"`
	}) (*struct {
		Body struct {
			Message string `json:"message"`
		}
	}, error) {
		return nil, nil
	})
	return api
}
