package crudx

import "testing"

func TestParseSort(t *testing.T) {
	cases := []struct {
		in      string
		wantCol string
		wantDir string
	}{
		{"created_at desc", "created_at", "desc"},
		{"name,desc", "name", "desc"},
		{"id", "id", "asc"},
		// doc.go / README 里的前缀写法
		{"-createdAt", "createdAt", "desc"},
		{"+createdAt", "createdAt", "asc"},
		{"-createdAt desc", "createdAt", "desc"},
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
	if p.sortByExplicit || p.sortDirExplicit {
		t.Fatalf("defaults must not be treated as explicit: %+v", p)
	}
}

func TestListParamsNormalize_startUpperBound(t *testing.T) {
	spec := ListSpec{Limits: Limits{LimitMax: 10}}
	p := &ListParams{Limit: 10, Start: 1000000000}
	spec.Normalize(p)
	// 默认 StartMax = LimitMax*100
	if p.Start != 1000 {
		t.Fatalf("start=%d want 1000", p.Start)
	}

	p2 := &ListParams{Limit: 10, Start: 5}
	spec.Normalize(p2)
	if p2.Start != 5 {
		t.Fatalf("start within bound should be preserved, got %d", p2.Start)
	}
}

func TestListParamsNormalize_explicitFlags(t *testing.T) {
	spec := ListSpec{SortFields: map[string]string{"createdAt": "created_at"}, DefaultSort: "-createdAt"}

	p := &ListParams{SortBy: "createdAt"}
	spec.Normalize(p)
	if !p.sortByExplicit || p.sortDirExplicit {
		t.Fatalf("flags=%v/%v", p.sortByExplicit, p.sortDirExplicit)
	}

	// 重复 Normalize 不能把补齐的默认值误当成显式输入。
	spec.Normalize(p)
	if !p.sortByExplicit || p.sortDirExplicit {
		t.Fatalf("after re-normalize flags=%v/%v", p.sortByExplicit, p.sortDirExplicit)
	}

	q := &ListParams{SortDirection: "desc"}
	spec.Normalize(q)
	if q.sortByExplicit || !q.sortDirExplicit {
		t.Fatalf("flags=%v/%v", q.sortByExplicit, q.sortDirExplicit)
	}
}
