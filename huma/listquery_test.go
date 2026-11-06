package huma

import (
	"testing"
)

func TestListParamsFromInput(t *testing.T) {
	// Huma 的 query 绑定已经解码过，传进来的就是最终值。
	in := &ListQueryInput{
		Q:              []string{"state=Running", "name~test"},
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

func TestListParamsFromInput_doesNotDecodeTwice(t *testing.T) {
	// 回归：早期实现对已解码的值再调一次 QueryUnescape，
	// 导致搜索字面量 "a%20b" 被改写成 "a b"。
	p, err := ListParamsFromInput(&ListQueryInput{Q: []string{"title~a%20b", " ", ""}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Q) != 1 || p.Q[0] != "title~a%20b" {
		t.Fatalf("got %v, want literal %%20 preserved", p.Q)
	}
}
