// Package openapi 合并 OpenAPI 3.x 文档（静态 base YAML + 可选 Huma live spec）。
package openapi

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// MergeYAML 解析 base 与覆盖层 spec（YAML 或 JSON）并返回合并后的 YAML。
// 覆盖层的 paths 与 components 条目会追加；键冲突时覆盖层优先。
func MergeYAML(base []byte, overlays ...[]byte) ([]byte, error) {
	doc, err := parseDoc(base)
	if err != nil {
		return nil, fmt.Errorf("openapi merge base: %w", err)
	}
	for i, raw := range overlays {
		if len(raw) == 0 {
			continue
		}
		over, err := parseDoc(raw)
		if err != nil {
			return nil, fmt.Errorf("openapi merge overlay %d: %w", i, err)
		}
		doc = mergeDocs(doc, over)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("openapi merge marshal: %w", err)
	}
	return out, nil
}

// MergeJSON 类似 [MergeYAML]，但接受 JSON 输入并返回 JSON。
func MergeJSON(base []byte, overlays ...[]byte) ([]byte, error) {
	yamlOut, err := MergeYAML(base, overlays...)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := yaml.Unmarshal(yamlOut, &doc); err != nil {
		return nil, err
	}
	return json.Marshal(doc)
}

// parseDoc 把 YAML/JSON 解析为顶层 map；空文档返回空 map 而不是 nil。
func parseDoc(raw []byte) (map[string]any, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// mergeDocs 合并两份文档：openapi 取较新版本，info/paths/components/tags/servers
// 分别合并，其余顶层键以覆盖层优先。
func mergeDocs(base, overlay map[string]any) map[string]any {
	out := cloneMap(base)
	if overlay == nil {
		return out
	}

	if v, ok := overlay["openapi"].(string); ok && v != "" {
		if cur, _ := out["openapi"].(string); cur == "" || versionGT(v, cur) {
			out["openapi"] = v
		}
	}

	out["info"] = mergeInfo(asMap(out["info"]), asMap(overlay["info"]))
	out["paths"] = mergeStringMaps(asMap(out["paths"]), asMap(overlay["paths"]))
	out["components"] = mergeComponents(asMap(out["components"]), asMap(overlay["components"]))
	out["tags"] = mergeTags(out["tags"], overlay["tags"])
	out["servers"] = mergeServers(out["servers"], overlay["servers"])

	// 其余顶层键（security / webhooks / externalDocs / jsonSchemaDialect 等）
	// 直接按「覆盖层优先」搬过去。早期实现只处理上面 6 个键，
	// overlay 里的 security 会被静默丢弃。
	for k, v := range overlay {
		switch k {
		case "openapi", "info", "paths", "components", "tags", "servers":
			continue
		}
		out[k] = cloneValue(v)
	}

	return out
}

// cloneValue 对 map/slice 做浅拷贝，避免合并结果与入参共享可变结构。
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case []any:
		out := make([]any, len(t))
		copy(out, t)
		return out
	default:
		return v
	}
}

// mergeInfo 逐键合并 info，覆盖层的键优先。
func mergeInfo(base, overlay map[string]any) map[string]any {
	if len(overlay) == 0 {
		return base
	}
	if len(base) == 0 {
		return cloneMap(overlay)
	}
	// 覆盖层优先：与 [MergeYAML] 的约定、以及同包 paths/components 的合并方向保持一致。
	// 早期实现是「只填空不覆盖」，导致内嵌占位骨架的 info.title 永远压过 Huma 的真实标题。
	out := cloneMap(base)
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// mergeStringMaps 合并同名键的 map，冲突项由 [mergePathItem] 处理。
func mergeStringMaps(base, overlay map[string]any) map[string]any {
	if len(overlay) == 0 {
		return base
	}
	out := cloneMap(base)
	for k, v := range overlay {
		if existing, ok := out[k]; ok {
			out[k] = mergePathItem(asMap(existing), asMap(v))
			continue
		}
		out[k] = v
	}
	return out
}

// mergePathItem 逐键合并 path item 或 component，覆盖层优先。
func mergePathItem(base, overlay map[string]any) map[string]any {
	if len(overlay) == 0 {
		return base
	}
	out := cloneMap(base)
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// componentSections 是 components 下参与合并的分类名；
// 合并规则：同名条目由覆盖层覆盖，未列出的分类只保留 base 内容。
var componentSections = []string{
	"schemas", "responses", "parameters", "examples",
	"requestBodies", "headers", "securitySchemes", "links", "callbacks",
}

// mergeComponents 合并 components 下各已知分类（schemas、responses 等）。
func mergeComponents(base, overlay map[string]any) map[string]any {
	if len(overlay) == 0 {
		return base
	}
	out := cloneMap(base)
	for _, section := range componentSections {
		merged := mergeStringMaps(asMap(out[section]), asMap(overlay[section]))
		if len(merged) > 0 {
			out[section] = merged
		}
	}
	return out
}

// mergeTags 按 name 去重合并 tags（base 在前，overlay 在后）。
func mergeTags(base, overlay any) any { return mergeByKey(base, overlay, "name") }

// mergeServers 按 url 去重合并 servers（base 在前，overlay 在后）。
func mergeServers(base, overlay any) any { return mergeByKey(base, overlay, "url") }

// mergeByKey 按 key 字段去重合并两个数组（base 在前，overlay 在后）。
// 缺少 key 的条目无法判断是否重复，原样保留。
func mergeByKey(base, overlay any, key string) any {
	baseList := asSlice(base)
	overlayList := asSlice(overlay)
	if len(overlayList) == 0 {
		return baseList
	}
	all := make([]any, 0, len(baseList)+len(overlayList))
	all = append(all, baseList...)
	all = append(all, overlayList...)

	seen := make(map[string]struct{}, len(all))
	out := make([]any, 0, len(all))
	for _, item := range all {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		k, _ := m[key].(string)
		if k == "" {
			out = append(out, item)
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// asSlice 把值断言为 []any，失败时返回 nil。
func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// asMap 把值断言为 map[string]any，失败时返回 nil。
func asMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

// cloneMap 浅拷贝 map；空输入返回空 map。
func cloneMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// versionGT 比较 OpenAPI 版本字符串（例如 "3.1.0" > "3.0.3"）。
func versionGT(a, b string) bool {
	if a == b {
		return false
	}
	ap := splitVersion(a)
	bp := splitVersion(b)
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] != bp[i] {
			return ap[i] > bp[i]
		}
	}
	return len(ap) > len(bp)
}

// splitVersion 提取版本字符串中的数字段（"3.1.0" → [3 1 0]）。
func splitVersion(v string) []int {
	var parts []int
	var n int
	hasDigit := false
	for _, c := range v {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
			hasDigit = true
			continue
		}
		if hasDigit {
			parts = append(parts, n)
			n = 0
			hasDigit = false
		}
	}
	if hasDigit {
		parts = append(parts, n)
	}
	return parts
}
