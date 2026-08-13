package huma

import (
	"errors"
	"testing"

	"github.com/fitan/fxkit/fxerrors"
)

func TestListParamsFromInput(t *testing.T) {
	in := &ListQueryInput{
		Q:              []string{"state%3DRunning", "name~test"},
		Limit:          50,
		Start:          10,
		SortBy:         "name",
		SortDirection:  "desc",
		ReplyWithCount: true,
	}
	params, err := ListParamsFromInput(in)
	if err != nil {
		t.Fatal(err)
	}
	if params.Limit != 50 || params.Start != 10 || !params.ReplyWithCount {
		t.Fatalf("got %+v", params)
	}
	if params.SortBy != "name" || params.SortDirection != "desc" {
		t.Fatalf("sort=%s %s", params.SortBy, params.SortDirection)
	}
	if len(params.Q) != 2 || params.Q[0] != "state=Running" || params.Q[1] != "name~test" {
		t.Fatalf("q=%v", params.Q)
	}
}

func TestListParamsFromInput_decodeError(t *testing.T) {
	_, err := ListParamsFromInput(&ListQueryInput{Q: []string{"%ZZ"}})
	if err == nil {
		t.Fatal("expected decode error")
	}
	var fe *fxerrors.Error
	if !errors.As(err, &fe) || fe.Details["reason"] != "URL_DECODE_FAILED" {
		t.Fatalf("got %v", err)
	}
}
