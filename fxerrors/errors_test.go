package fxerrors_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
)

func TestKindStatus(t *testing.T) {
	if got := fxerrors.KindNotFound.Status(); got != http.StatusNotFound {
		t.Fatalf("NotFound status=%d", got)
	}
	if got := fxerrors.Kind("nope").Status(); got != http.StatusInternalServerError {
		t.Fatalf("unknown status=%d", got)
	}
}

func TestWrapPreservesMessageAndCause(t *testing.T) {
	cause := errors.New("secret db failure")
	err := fxerrors.Wrap(cause)
	if err.Message != "secret db failure" {
		t.Fatalf("message=%q", err.Message)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("cause not preserved: %v", err.Unwrap())
	}
	if !fxerrors.Is(err, fxerrors.KindInternal) {
		t.Fatal("expected KindInternal")
	}

	// When serialized for HTTP clients, WriteError sanitizes KindInternal to "internal error"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	fxerrors.WriteError(rec, req, err)
	if strings.Contains(rec.Body.String(), "secret db failure") {
		t.Fatalf("leaked internal message to client: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"message":"internal error"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestWrapPreservesFxError(t *testing.T) {
	orig := fxerrors.NotFound("user", "id=%d", 1)
	wrapped := fxerrors.Wrap(orig)
	if wrapped != orig {
		t.Fatal("Wrap should return same *Error")
	}
	if !fxerrors.Is(wrapped, fxerrors.KindNotFound) {
		t.Fatal("expected KindNotFound")
	}
}

func TestWithDetailsClones(t *testing.T) {
	base := fxerrors.Conflict("dup")
	next := base.WithDetails(map[string]any{"email": "a@b.c"})
	if base.Details != nil {
		t.Fatal("base should remain without details")
	}
	if next.Details["email"] != "a@b.c" {
		t.Fatalf("details=%v", next.Details)
	}
}

func TestWriteErrorHidesInternalMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	fxerrors.WriteError(rec, req, fxerrors.Internal("postgres ping failed at 10.0.0.5"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "postgres") || strings.Contains(body, "10.0.0.5") {
		t.Fatalf("leaked internal message: %s", body)
	}
	if !strings.Contains(body, `"message":"internal error"`) {
		t.Fatalf("body=%s", body)
	}
}

func TestWriteErrorPreservesNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	fxerrors.WriteError(rec, req, fxerrors.NotFound("user", "id=%d", 1))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "user not found") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
