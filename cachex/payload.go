package cachex

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// payloadMagic 标记 GetOrSet 写入的带类型指纹的缓存 payload。
//
// 读取时兼容旧格式（直接 JSON），但新写入都会带类型指纹，避免
// GetOrSet[Order] 读到同一 key 下 User 的 JSON 后静默反序列化为零值。
var payloadMagic = []byte("<<__FXKIT_CACHE_PAYLOAD_V1__>>")

type payloadEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// typeFingerprint 返回泛型实参的类型指纹，用于 singleflight key 与 payload。
func typeFingerprint[T any]() string {
	return fingerprintOf(reflect.TypeFor[T]())
}

func fingerprintOf(t reflect.Type) string {
	if t == nil {
		return "<nil>"
	}
	if t.Name() != "" {
		// 同名类型可能来自不同包，PkgPath 一起拼上避免碰撞。
		return t.PkgPath() + "." + t.Name()
	}
	return t.String()
}

func encodeValue[T any](v T) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payloadEnvelope{
		Type: typeFingerprint[T](),
		Data: raw,
	})
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(payloadMagic)+len(body))
	out = append(out, payloadMagic...)
	out = append(out, body...)
	return out, nil
}

// decodeValue 反序列化缓存 payload。
//
// 返回 ok=false 表示 payload 不是当前类型的有效数据（类型不匹配或脏数据），
// 调用方应回源 loader 并覆盖缓存。
func decodeValue[T any](data []byte) (T, bool) {
	var zero T
	if !bytes.HasPrefix(data, payloadMagic) {
		// 兼容没有信封的旧数据。
		var legacy T
		if err := json.Unmarshal(data, &legacy); err != nil {
			return zero, false
		}
		return legacy, true
	}

	var env payloadEnvelope
	if err := json.Unmarshal(data[len(payloadMagic):], &env); err != nil {
		return zero, false
	}
	if env.Type != typeFingerprint[T]() {
		return zero, false
	}
	var v T
	if err := json.Unmarshal(env.Data, &v); err != nil {
		return zero, false
	}
	return v, true
}
