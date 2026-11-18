package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPprofDisabledByDefault(t *testing.T) {
	mux := NewMux(MuxParams{Server: &Config{}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled pprof status=%d, want 404", rec.Code)
	}
}

func TestPprofEnabled(t *testing.T) {
	mux := NewMux(MuxParams{Server: &Config{Pprof: PprofConfig{Enabled: true, Prefix: "/debug/pprof/"}}})
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/goroutine"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d, want 200", path, rec.Code)
		}
	}
}
