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

	return out
}

func mergeInfo(base, overlay map[string]any) map[string]any {
	if len(overlay) == 0 {
		return base
	}
	if len(base) == 0 {
		return cloneMap(overlay)
	}
	out := cloneMap(base)
	for k, v := range overlay {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}

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

var componentSections = []string{
	"schemas", "responses", "parameters", "examples",
	"requestBodies", "headers", "securitySchemes", "links", "callbacks",
}

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

func mergeTags(base, overlay any) any {
	baseTags := tagSlice(base)
	overlayTags := tagSlice(overlay)
	if len(overlayTags) == 0 {
		return baseTags
	}
	seen := map[string]struct{}{}
	var out []any
	for _, t := range append(baseTags, overlayTags...) {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			out = append(out, t)
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeServers(base, overlay any) any {
	baseList := serverSlice(base)
	overlayList := serverSlice(overlay)
	if len(overlayList) == 0 {
		return baseList
	}
	seen := map[string]struct{}{}
	var out []any
	for _, s := range append(baseList, overlayList...) {
		m, ok := s.(map[string]any)
		if !ok {
			continue
		}
		url, _ := m["url"].(string)
		if url == "" {
			out = append(out, s)
			continue
		}
		if _, dup := seen[url]; dup {
			continue
		}
		seen[url] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func tagSlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func serverSlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

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
