package crudx

import "testing"

func TestParseSort(t *testing.T) {
	cases := []struct {
		in       string
		wantCol  string
		wantDir  string
	}{
		{"created_at desc", "created_at", "desc"},
		{"name,desc", "name", "desc"},
		{"id", "id", "asc"},
	}
	for _, tc := range cases {
		col, dir := ParseSort(tc.in)
		if col != tc.wantCol || dir != tc.wantDir {
			t.Errorf("ParseSort(%q) = %q,%q; want %q,%q", tc.in, col, dir, tc.wantCol, tc.wantDir)
		}
	}
}

func TestValidatedSortPath(t *testing.T) {
	spec := ListSpec{
		SortFields: map[string]string{
			"id":         "users.id",
			"created_at": "users.created_at",
		},
		DefaultSort: "created_at desc",
	}
	col, dir, ok := validatedSortPath(spec, "id", "asc")
	if !ok || col != "users.id" || dir != "asc" {
		t.Fatalf("got col=%q dir=%q ok=%v", col, dir, ok)
	}
	_, _, ok = validatedSortPath(spec, "name;drop table", "asc")
	if ok {
		t.Fatal("expected injection attempt to be rejected")
	}
	col, dir, ok = validatedSortPath(spec, "", "")
	if !ok || col != "users.created_at" || dir != "desc" {
		t.Fatalf("default sort: col=%q dir=%q ok=%v", col, dir, ok)
	}
}

func TestListParamsNormalize(t *testing.T) {
	spec := ListSpec{DefaultSort: "id desc", Limits: Limits{LimitDefault: 50, LimitMax: 500}}
	p := &ListParams{}
	spec.Normalize(p)
	if p.Limit != 50 {
		t.Fatalf("limit=%d", p.Limit)
	}
	if p.SortBy != "id" || p.SortDirection != "desc" {
		t.Fatalf("sort=%s %s", p.SortBy, p.SortDirection)
	}
}
