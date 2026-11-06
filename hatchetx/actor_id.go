package hatchetx

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/tidwall/gjson"
)

// actorIDOf 从输入中取 actorId。契约只有三种：
//   - 嵌入 hatchetx.ActorRef（其 ActorID 字段带 json:"actorId"）
//   - 任意结构体字段带 json:"actorId" 标签
//   - 原始 JSON 字节（[]byte / json.RawMessage）
//
// 曾经还支持 map[string]any/map[string]string 与「实现 GetActorID()」两条兼容路径，
// 以及一个 hasNilEmbeddedPtr 反射守卫。它们的收益很小（map 输入可以用结构体代替，
// GetActorID 无法保证 JSON 里有 actorId），却让提取逻辑分成三条路径。现在归为一条。
func actorIDOf(in any) string {
	switch v := in.(type) {
	case nil:
		return ""
	case json.RawMessage:
		return jsonActorID(v)
	case []byte:
		return jsonActorID(v)
	}
	return structActorID(reflect.ValueOf(in))
}

func jsonActorID(raw []byte) string {
	return strings.TrimSpace(gjson.GetBytes(raw, "actorId").String())
}

// structActorID 在结构体（含匿名嵌入）里按字段名 / json 标签找 actorId。
func structActorID(v reflect.Value) string {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Tag.Get("json") == "-" {
			continue
		}
		fv := v.Field(i)
		if f.Anonymous {
			if id := structActorID(fv); id != "" {
				return id
			}
			continue
		}
		if !isActorIDField(f) {
			continue
		}
		for fv.Kind() == reflect.Pointer || fv.Kind() == reflect.Interface {
			if fv.IsNil() {
				return ""
			}
			fv = fv.Elem()
		}
		if fv.Kind() == reflect.String {
			return strings.TrimSpace(fv.String())
		}
	}
	return ""
}

func isActorIDField(f reflect.StructField) bool {
	if f.Name == "ActorID" {
		return true
	}
	tag := f.Tag.Get("json")
	return tag == "actorId" || strings.HasPrefix(tag, "actorId,")
}

// typeHasJSONActorID 报告类型 T（含匿名嵌入）是否有字段会序列化成 "actorId"。
func typeHasJSONActorID(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	// 自定义序列化：静态分析看不到最终 JSON，保守返回 false，
	// 让 validateActorID 走实际序列化校验。
	marshaler := reflect.TypeFor[json.Marshaler]()
	if t.Implements(marshaler) || reflect.PointerTo(t).Implements(marshaler) {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Tag.Get("json") == "-" {
			continue
		}
		if f.Anonymous {
			if typeHasJSONActorID(f.Type) {
				return true
			}
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "actorId" || strings.HasPrefix(tag, "actorId,") {
			return true
		}
	}
	return false
}

// typeHasActorIDField 报告类型是否声明了 ActorID 字段（含匿名嵌入）。
// 用于在 NewActor 时提前告警「有 id 但不会序列化成 actorId」。
func typeHasActorIDField(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && typeHasActorIDField(f.Type) {
			return true
		}
		if f.Name == "ActorID" {
			return true
		}
	}
	return false
}

func requireActorID(in any) (string, error) {
	id := actorIDOf(in)
	if id == "" {
		return "", fmt.Errorf("hatchetx: actorId is required (embed hatchetx.ActorRef or field with json:\"actorId\")")
	}
	if err := assertActorIDInJSON(in, id); err != nil {
		return "", err
	}
	return id, nil
}

// assertActorIDInJSON 确保 Hatchet CEL input.actorId 看到的 id 与 Call 校验的一致。
// 使用 gjson 从原始 JSON 字节中高性能、零分配地提取。
func assertActorIDInJSON(in any, want string) error {
	var raw []byte
	switch v := in.(type) {
	case []byte:
		raw = v
	case json.RawMessage:
		raw = v
	default:
		var err error
		raw, err = json.Marshal(in)
		if err != nil {
			return fmt.Errorf("hatchetx: marshal actor input: %w", err)
		}
	}

	res := gjson.GetBytes(raw, "actorId")
	if !res.Exists() || strings.TrimSpace(res.String()) == "" {
		return fmt.Errorf("hatchetx: input must JSON-serialize \"actorId\" for concurrency (embed hatchetx.ActorRef or json:\"actorId\"); GetActorID alone is not enough")
	}
	if got := strings.TrimSpace(res.String()); got != want {
		return fmt.Errorf("hatchetx: json actorId %q disagrees with GetActorID/ActorID %q", got, want)
	}
	return nil
}
