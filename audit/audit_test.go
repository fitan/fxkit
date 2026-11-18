package audit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fitan/fxkit/audit"
	"github.com/fitan/fxkit/authz"
)

type fakeSink struct {
	events []audit.Event
}

func (f *fakeSink) Write(_ context.Context, ev audit.Event) error {
	f.events = append(f.events, ev)
	return nil
}

func TestMiddlewareRecordsEvent(t *testing.T) {
	sink := &fakeSink{}
	h := audit.Middleware(audit.Config{Enabled: true, Sink: "slog"}, sink)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/articles", nil)
	req = req.WithContext(authz.WithSubject(req.Context(), authz.Subject{ID: "alice", OrgID: "demo"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if len(sink.events) != 1 {
		t.Fatalf("events=%d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Actor != "alice" || ev.OrgID != "demo" {
		t.Fatalf("actor/org=%q/%q", ev.Actor, ev.OrgID)
	}
	if ev.Method != http.MethodPost || ev.Path != "/articles" || ev.Status != http.StatusCreated {
		t.Fatalf("event=%+v", ev)
	}
}

func TestMiddlewareDisabledPassesThrough(t *testing.T) {
	sink := &fakeSink{}
	h := audit.Middleware(audit.Config{Enabled: false}, sink)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d", rec.Code)
	}
	if len(sink.events) != 0 {
		t.Fatalf("events=%d, want 0", len(sink.events))
	}
}

func TestMiddlewareRedactsHeadersAndCapturesBody(t *testing.T) {
	sink := &fakeSink{}
	h := audit.Middleware(audit.Config{
		Enabled:        true,
		Sink:           "slog",
		IncludeBody:    true,
		IncludeHeaders: true,
		RedactHeaders:  []string{"Authorization"},
	}, sink)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"ok":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	ev := sink.events[0]
	if ev.Metadata["body"] != `{"ok":true}` {
		t.Fatalf("body=%v", ev.Metadata["body"])
	}
	headers, _ := ev.Metadata["headers"].(map[string]string)
	if headers["Authorization"] != "[redacted]" {
		t.Fatalf("headers=%v", headers)
	}
}
