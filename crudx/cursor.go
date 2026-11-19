package crudx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/fitan/fxkit/fxerrors"
)

// cursorPayload 是不透明的列表游标（排序值 + 用于打破平局的主键）。
type cursorPayload struct {
	// V 是上一页最后一行的排序值，须为标量。
	V any `json:"v"`
	// PK 是用于打破平局的主键值（字符串形式）。
	PK string `json:"pk"`
	SB string `json:"sb,omitempty"` // 编码时的 sortBy
	SD string `json:"sd,omitempty"` // 排序方向
	// VK 是排序值的类型（time|number|string|null）。早期版本没有这个字段，
	// 只能靠字符串格式猜测；varchar 列存了形似时间的值会被错误转成 time.Time。
	VK string `json:"vk,omitempty"`
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
		VK: cursorValueKind(sortValue),
	})
	if err != nil {
		return "", fxerrors.Wrap(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// cursorValueKind 把排序值归类为游标里的类型标签。
func cursorValueKind(v any) string {
	if v == nil {
		return "null"
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return "null"
		}
		rv = rv.Elem()
	}
	if _, ok := rv.Interface().(time.Time); ok {
		return "time"
	}
	switch rv.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	}
	return "other"
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
	// 排序值必须是标量：对象/数组绑定到 SQL 参数会被驱动拒绝（500），
	// 而这类游标是客户端可以任意构造的。
	if !isCursorScalar(p.V) {
		return "", "", nil, "", fxerrors.BadRequest("cursor: sort value must be a scalar")
	}
	sv, err := decodeCursorValue(p.V, p.VK)
	if err != nil {
		return "", "", nil, "", err
	}
	return p.SB, normalizeSortDir(p.SD), sv, p.PK, nil
}

// isCursorScalar 报告 v 是否为游标允许的标量类型（nil、string、json.Number、bool）。
func isCursorScalar(v any) bool {
	switch v.(type) {
	case nil, string, json.Number, bool:
		return true
	}
	return false
}

// decodeCursorValue 按类型标签还原排序值；VK 为空时走旧版启发式（向后兼容）。
func decodeCursorValue(v any, kind string) (any, error) {
	switch kind {
	case "time":
		s, ok := v.(string)
		if !ok {
			return nil, fxerrors.BadRequest("cursor: invalid time value")
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, fxerrors.BadRequest("cursor: invalid time value")
		}
		return t, nil
	case "string":
		s, ok := v.(string)
		if !ok {
			return nil, fxerrors.BadRequest("cursor: invalid string value")
		}
		return s, nil
	case "number":
		n, ok := v.(json.Number)
		if !ok {
			return nil, fxerrors.BadRequest("cursor: invalid number value")
		}
		if i, err := n.Int64(); err == nil {
			return i, nil
		}
		if f, err := n.Float64(); err == nil {
			return f, nil
		}
		return nil, fxerrors.BadRequest("cursor: invalid number value")
	case "null":
		return nil, nil
	case "other":
		return v, nil
	default:
		return normalizeCursorValue(v), nil
	}
}

// normalizeCursorValue 是旧版游标（无 VK 类型标签）的取值启发式：数字转 int64/float64，
// 可解析为 RFC3339 的字符串转 time.Time，其余原样返回。
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
