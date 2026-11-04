package crudx

import (
	"errors"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
)

// TestListErrorReason 确保指标用的是稳定原因码，而不是原始错误文本（高基数）。
func TestListErrorReason(t *testing.T) {
	reasoned := InvalidQueryCondition("q=bad", ReasonFieldNotExist)
	if got := listErrorReason(reasoned); got != ReasonFieldNotExist {
		t.Fatalf("reason = %q, want %q", got, ReasonFieldNotExist)
	}

	kindOnly := fxerrors.Conflict("duplicate")
	if got := listErrorReason(kindOnly); got != string(fxerrors.KindConflict) {
		t.Fatalf("reason = %q, want kind %q", got, fxerrors.KindConflict)
	}

	if got := listErrorReason(errors.New("boom")); got != "internal" {
		t.Fatalf("reason = %q, want internal", got)
	}

	if got := listErrorReason(nil); got != "ok" {
		t.Fatalf("nil reason = %q, want ok", got)
	}
}
