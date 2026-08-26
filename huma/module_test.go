package huma

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fitan/fxkit/config"
	"github.com/go-chi/chi/v5"
)

func TestNewAPI_OpenAPIJSONPath(t *testing.T) {
	mux := chi.NewRouter()
	api := NewAPI(mux, &config.App{Name: "t"})
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
