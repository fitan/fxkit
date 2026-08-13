package crudx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fitan/fxkit/fxerrors"
)

// cursorPayload is the opaque list cursor (sort value + primary key tie-breaker).
type cursorPayload struct {
	V  any    `json:"v"`
	PK string `json:"pk"`
	SB string `json:"sb,omitempty"` // sortBy at encode time
	SD string `json:"sd,omitempty"` // sortDirection
}

// EncodeCursor builds an opaque cursor token for keyset pagination.
func EncodeCursor(sortBy, sortDir string, sortValue any, pk string) (string, error) {
	pk = strings.TrimSpace(pk)
	if pk == "" {
		return "", fxerrors.BadRequest("cursor: empty primary key")
	}
	raw, err := json.Marshal(cursorPayload{
		V:  sortValue,
		PK: pk,
		SB: sortBy,
		SD: normalizeSortDir(sortDir),
	})
	if err != nil {
		return "", fxerrors.Wrap(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// DecodeCursor parses an opaque cursor token.
func DecodeCursor(token string) (sortBy, sortDir string, sortValue any, pk string, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", "", nil, "", fxerrors.BadRequest("cursor: empty")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", "", nil, "", fxerrors.BadRequest("cursor: invalid encoding")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var p cursorPayload
	if err := dec.Decode(&p); err != nil {
		return "", "", nil, "", fxerrors.BadRequest("cursor: invalid payload")
	}
	if strings.TrimSpace(p.PK) == "" {
		return "", "", nil, "", fxerrors.BadRequest("cursor: missing pk")
	}
	return p.SB, normalizeSortDir(p.SD), normalizeCursorValue(p.V), p.PK, nil
}

func normalizeCursorValue(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	default:
		return v
	}
}

func cursorWhereSQL(sortCol, pkCol, dir string) (string, error) {
	dir = normalizeSortDir(dir)
	if dir == "" {
		return "", fmt.Errorf("crudx: invalid sort direction")
	}
	if dir == "asc" {
		return fmt.Sprintf("(%s > ? OR (%s = ? AND %s > ?))", sortCol, sortCol, pkCol), nil
	}
	return fmt.Sprintf("(%s < ? OR (%s = ? AND %s < ?))", sortCol, sortCol, pkCol), nil
}
