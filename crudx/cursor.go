package crudx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fitan/fxkit/fxerrors"
)

// cursorPayload 是不透明的列表游标（排序值 + 用于打破平局的主键）。
type cursorPayload struct {
	V  any    `json:"v"`
	PK string `json:"pk"`
	SB string `json:"sb,omitempty"` // 编码时的 sortBy
	SD string `json:"sd,omitempty"` // 排序方向
}

// EncodeCursor 为 keyset 分页构建不透明的游标 token。
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

// DecodeCursor 解析不透明的游标 token。
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
	case string:
		if tm, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return tm
		}
		if tm, err := time.Parse(time.RFC3339, t); err == nil {
			return tm
		}
		return t
	default:
		return v
	}
}

// cursorWhereSQL 为下一页构建 keyset 谓词。
//
// 排序统一把 NULL 放在最后（见 applyList 的 "(col IS NULL)" 首列），因此：
//   - sortVal 为 nil 时表示已经翻到 NULL 分组，只需按主键继续翻；
//   - sortVal 非 nil 时还要把 NULL 分组一并纳入（它们排在所有非 NULL 值之后）。
func cursorWhereSQL(sortCol, pkCol, dir string, sortVal, pk any) (string, []any, error) {
	dir = normalizeSortDir(dir)
	if dir == "" {
		return "", nil, fmt.Errorf("crudx: invalid sort direction")
	}
	cmp := ">"
	if dir == "desc" {
		cmp = "<"
	}
	if sortVal == nil {
		return fmt.Sprintf("(%s IS NULL AND %s %s ?)", sortCol, pkCol, cmp), []any{pk}, nil
	}
	return fmt.Sprintf("(%s %s ? OR (%s = ? AND %s %s ?) OR %s IS NULL)",
		sortCol, cmp, sortCol, pkCol, cmp, sortCol), []any{sortVal, sortVal, pk}, nil
}
