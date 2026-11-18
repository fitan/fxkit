package huma

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	humav2 "github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/go-chi/chi/v5"
)

type schemaTestOutput struct {
	Body struct {
		Name string `json:"name"`
	}
}

func registerSchemaTestOp(api humav2.API) {
	humav2.Register(api, humav2.Operation{
		OperationID: "schemaTest",
		Method:      http.MethodGet,
		Path:        "/schema-test",
	}, func(context.Context, *struct{}) (*schemaTestOutput, error) {
		return &schemaTestOutput{}, nil
	})
}

func TestNewAPI_OpenAPIDisabledByDefault(t *testing.T) {
	mux := chi.NewRouter()
	api := NewAPI(mux, &config.App{Name: "t"})
	if api == nil {
		t.Fatal("nil api")
	}
	if api.OpenAPI() == nil {
		t.Fatal("nil in-memory spec")
	}

	req := httptest.NewRequest(http.MethodGet, "/huma/openapi.json", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /huma/openapi.json status=%d, want 404 (spec must be opt-in)", rec.Code)
	}
}

func TestNewAPI_SchemaLinksDisabledByDefault(t *testing.T) {
	mux := chi.NewRouter()
	api := NewAPI(mux, &config.App{Name: "t"})
	registerSchemaTestOp(api)

	b, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"$schema"`) {
		t.Fatal("$schema must not be injected by default")
	}
}

func TestNewAPI_SchemaLinksEnabled(t *testing.T) {
	mux := chi.NewRouter()
	api := NewAPI(mux, &config.App{Name: "t"}, &Config{SchemaLinks: true})
	registerSchemaTestOp(api)

	b, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"$schema"`) {
		t.Fatal("$schema should be injected when schema_links=true")
	}
}

func TestNewAPI_SchemaLinksResponseDisabledByDefault(t *testing.T) {
	mux := chi.NewRouter()
	api := NewAPI(mux, &config.App{Name: "t"})
	registerSchemaTestOp(api)

	req := httptest.NewRequest(http.MethodGet, "/schema-test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /schema-test status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"$schema"`) {
		t.Fatalf("response must not contain $schema: %s", rec.Body.String())
	}
}

func TestNewAPI_SchemaLinksResponseEnabled(t *testing.T) {
	mux := chi.NewRouter()
	api := NewAPI(mux, &config.App{Name: "t"}, &Config{SchemaLinks: true})
	registerSchemaTestOp(api)

	req := httptest.NewRequest(http.MethodGet, "/schema-test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /schema-test status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"$schema"`) {
		t.Fatalf("response should contain $schema when schema_links=true: %s", rec.Body.String())
	}
}

func TestNewAPI_OpenAPIJSONPath(t *testing.T) {
	mux := chi.NewRouter()
	api := NewAPI(mux, &config.App{Name: "t"}, &Config{PublicSpec: true})
	if api == nil {
		t.Fatal("nil api")
	}

	req := httptest.NewRequest(http.MethodGet, "/huma/openapi.json", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /huma/openapi.json status=%d body=%s", rec.Code, rec.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	if doc["openapi"] == nil {
		t.Fatalf("missing openapi field: %v", doc)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/huma/openapi.json.json", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code == http.StatusOK {
		t.Fatal("Huma OpenAPIPath included an extra .json suffix")
	}
}
