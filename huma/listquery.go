package huma

import (
	"net/http"

	"github.com/fitan/fxkit/crudx"
)

// ListQueryInput is the standard Huma GET list query parameter set (ZStack-style).
type ListQueryInput struct {
	Q              []string `query:"q,explode" doc:"Filter conditions (AND). Example: state=Running. OR group: or(a=1,b=2)"`
	Limit          int      `query:"limit" default:"100" minimum:"1" maximum:"1000" doc:"Max rows per page"`
	Start          int      `query:"start" default:"0" minimum:"0" doc:"Offset from 0 (ignored when cursor is set)"`
	SortBy         string   `query:"sortBy" doc:"Sort field, supports relation paths e.g. cluster.name"`
	SortDirection  string   `query:"sortDirection" enum:"asc,desc" default:"asc"`
	ReplyWithCount bool     `query:"replyWithCount" default:"false" doc:"Include total count (not with cursor)"`
	Cursor         string   `query:"cursor" doc:"Opaque keyset cursor from previous nextCursor"`
	UseCursor      bool     `query:"useCursor" default:"false" doc:"Return nextCursor on first page"`
}

// ListParamsFromInput maps Huma-bound query params to [crudx.ListParams].
// Q values are URL-decoded via [crudx.ParseHTTPQValues].
func ListParamsFromInput(in *ListQueryInput) (crudx.ListParams, error) {
	q, err := crudx.ParseHTTPQValues(in.Q)
	if err != nil {
		return crudx.ListParams{}, err
	}
	return crudx.ListParams{
		Limit:          in.Limit,
		Start:          in.Start,
		SortBy:         in.SortBy,
		SortDirection:  in.SortDirection,
		ReplyWithCount: in.ReplyWithCount,
		Q:              q,
		Cursor:         in.Cursor,
		UseCursor:      in.UseCursor,
	}, nil
}

// ParseHTTPListParams reads list params from an http.Request (for non-Huma handlers).
func ParseHTTPListParams(r *http.Request) (crudx.ListParams, error) {
	q := r.URL.Query()["q"]
	decoded, err := crudx.ParseHTTPQValues(q)
	if err != nil {
		return crudx.ListParams{}, err
	}
	limit := parseIntDefault(r.URL.Query().Get("limit"), 100)
	if limit > 1000 {
		limit = 1000
	}
	start := parseIntDefault(r.URL.Query().Get("start"), 0)
	reply := r.URL.Query().Get("replyWithCount") == "true"
	useCursor := r.URL.Query().Get("useCursor") == "true"
	return crudx.ListParams{
		Limit:          limit,
		Start:          start,
		SortBy:         r.URL.Query().Get("sortBy"),
		SortDirection:  r.URL.Query().Get("sortDirection"),
		ReplyWithCount: reply,
		Q:              decoded,
		Cursor:         r.URL.Query().Get("cursor"),
		UseCursor:      useCursor,
	}, nil
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return def
		}
	}
	if n <= 0 && s != "0" {
		return def
	}
	return n
}
