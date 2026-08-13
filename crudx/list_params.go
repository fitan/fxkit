package crudx

import "strings"

// ListParams carries ZStack-style list query parameters (after URL decode for Q values).
type ListParams struct {
	Limit          int
	Start          int
	SortBy         string
	SortDirection  string // asc | desc
	ReplyWithCount bool
	Q              []string
	Cursor         string // opaque keyset cursor; when set, Start is ignored
	UseCursor      bool   // request nextCursor even on the first page (no Cursor yet)
}

// Limits configures guardrails for list queries.
type Limits struct {
	MaxConditions int
	MaxInValues   int
	MaxJoins      int
	MaxLikeConds  int
	LimitDefault  int
	LimitMax      int
}

func (l Limits) normalized() Limits {
	out := l
	if out.MaxConditions <= 0 {
		out.MaxConditions = 20
	}
	if out.MaxInValues <= 0 {
		out.MaxInValues = 50
	}
	if out.MaxJoins <= 0 {
		out.MaxJoins = 3
	}
	if out.MaxLikeConds <= 0 {
		out.MaxLikeConds = 3
	}
	if out.LimitDefault <= 0 {
		out.LimitDefault = 100
	}
	if out.LimitMax <= 0 {
		out.LimitMax = 1000
	}
	return out
}

// Normalize applies defaults and caps from spec to params (mutates p).
func (spec *ListSpec) Normalize(p *ListParams) {
	lim := spec.Limits.normalized()
	if p.Limit <= 0 {
		p.Limit = lim.LimitDefault
	}
	if p.Limit > lim.LimitMax {
		p.Limit = lim.LimitMax
	}
	if p.Start < 0 {
		p.Start = 0
	}
	p.SortBy = strings.TrimSpace(p.SortBy)
	p.SortDirection = normalizeSortDir(p.SortDirection)
	if p.SortBy == "" && spec.DefaultSort != "" {
		p.SortBy, p.SortDirection = ParseSort(spec.DefaultSort)
	}
	p.Cursor = strings.TrimSpace(p.Cursor)
	if p.Cursor != "" {
		p.UseCursor = true
		p.Start = 0
	}
}

// deriveSortFields copies Spec and fills SortFields from Fields when a name is missing.
// Does not mutate the caller's map.
func deriveSortFields(spec ListSpec) ListSpec {
	if len(spec.Fields) == 0 {
		return spec
	}
	out := spec
	sf := make(map[string]string, len(spec.SortFields)+len(spec.Fields))
	for k, v := range spec.SortFields {
		sf[k] = v
	}
	for name, f := range spec.Fields {
		if _, ok := sf[name]; ok {
			continue
		}
		if f.Column == "" {
			continue
		}
		sf[name] = f.Column
	}
	out.SortFields = sf
	return out
}
