package huma

import (
	"net/http"
	"strings"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
)

func TestAsErrorHidesInternalMessage(t *testing.T) {
	err := AsError(fxerrors.Internal("auth casbin not enabled"))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, "casbin") {
		t.Fatalf("leaked internal message: %s", msg)
	}
	if !strings.Contains(msg, "internal error") {
		t.Fatalf("got %q", msg)
	}
}

func TestAsErrorPreservesNotFound(t *testing.T) {
	err := AsError(fxerrors.NotFound("user", "id=%s", "1"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "user not found") {
		t.Fatalf("got %q", err.Error())
	}
}

func TestAsErrorPlainError(t *testing.T) {
	err := AsError(http.ErrHandlerTimeout)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("got %q", err.Error())
	}
}
