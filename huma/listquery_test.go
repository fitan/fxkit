package huma

import (
	"testing"
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
	// 非法百分号转义现在是字面量（搜索词按原文匹配），不再打 400。
	p, err := ListParamsFromInput(&ListQueryInput{Q: []string{"%ZZ"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Q) != 1 || p.Q[0] != "%ZZ" {
		t.Fatalf("got %v, want literal %%ZZ", p.Q)
	}
}
