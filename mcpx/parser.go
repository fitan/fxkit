package mcpx

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"gopkg.in/yaml.v3"
)

var invalidIdentRegexp = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// ParseOpenAPI 从 OpenAPI 3.0 或 3.1 YAML/JSON 字节流中提取所有 operations 并转为 []Tool。
func ParseOpenAPI(data []byte) ([]Tool, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		if errYAML := yaml.Unmarshal(data, &root); errYAML != nil {
			return nil, fmt.Errorf("mcpx: parse openapi failed: %w", err)
		}
	}

	pathsVal, ok := root["paths"]
	if !ok {
		return nil, nil
	}
	paths, ok := pathsVal.(map[string]any)
	if !ok {
		return nil, nil
	}

	var tools []Tool
	httpMethods := []string{"get", "post", "put", "delete", "patch"}

	// 排序保证生成的 tool 名称与顺序稳定（map 迭代顺序随机）。
	pathKeys := make([]string, 0, len(paths))
	for p := range paths {
		pathKeys = append(pathKeys, p)
	}
	sort.Strings(pathKeys)

	usedNames := make(map[string]int, len(pathKeys))
	for _, pathStr := range pathKeys {
		pathItem, ok := paths[pathStr].(map[string]any)
		if !ok {
			continue
		}

		// 检查 path 级参数
		var pathLevelParams []any
		if plp, ok := pathItem["parameters"].([]any); ok {
			pathLevelParams = plp
		}

		for _, method := range httpMethods {
			opVal, exists := pathItem[method]
			if !exists {
				continue
			}
			op, ok := opVal.(map[string]any)
			if !ok {
				continue
			}

			tool, err := extractOperation(root, strings.ToUpper(method), pathStr, op, pathLevelParams)
			if err != nil {
				return nil, err
			}
			// 名称冲突（不同 path 归一到同一标识符）时加后缀，
			// 避免注册到 map 时后写静默覆盖前者。
			if _, seen := usedNames[tool.Name]; seen {
				base := tool.Name
				for i := 2; ; i++ {
					candidate := fmt.Sprintf("%s_%d", base, i)
					if _, exists := usedNames[candidate]; !exists {
						tool.Name = candidate
						break
					}
				}
			}
			usedNames[tool.Name] = 1
			tools = append(tools, tool)
		}
	}

	return tools, nil
}

// ParseHuma 直接从运行时注册的 Huma API 实例中自动提取 Tool 定义。
func ParseHuma(api huma.API) ([]Tool, error) {
	if api == nil || api.OpenAPI() == nil {
		return nil, fmt.Errorf("mcpx: nil huma api")
	}
	b, err := api.OpenAPI().YAML()
	if err != nil {
		return nil, fmt.Errorf("mcpx: dump huma openapi: %w", err)
	}
	return ParseOpenAPI(b)
}

func extractOperation(root map[string]any, method, path string, op map[string]any, parentParams []any) (Tool, error) {
	// 1. 获取 Tool 标识名称 (Name)
	name := ""
	if opID, ok := op["operationId"].(string); ok && strings.TrimSpace(opID) != "" {
		name = strings.TrimSpace(opID)
	}
	if name == "" {
		cleanPath := strings.ReplaceAll(path, "/", "_")
		cleanPath = strings.ReplaceAll(cleanPath, "{", "")
		cleanPath = strings.ReplaceAll(cleanPath, "}", "")
		name = strings.ToLower(method) + "_" + strings.Trim(cleanPath, "_")
	}
	name = invalidIdentRegexp.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")

	// 2. 获取描述 (Description)
	desc := ""
	if d, ok := op["description"].(string); ok && strings.TrimSpace(d) != "" {
		desc = strings.TrimSpace(d)
	} else if s, ok := op["summary"].(string); ok && strings.TrimSpace(s) != "" {
		desc = strings.TrimSpace(s)
	} else {
		desc = fmt.Sprintf("Execute %s %s", method, path)
	}

	// 3. 构建 InputSchema (JSON Schema object) 与参数位置映射
	properties := make(map[string]any)
	var requiredFields []string
	paramLocations := make(map[string]string)

	// 合并 parameters（path, query, header）。
	// 同一 name+in 的 operation 级参数覆盖 path 级（OpenAPI 语义）。
	var (
		ordered   []map[string]any
		paramSeen = map[string]int{}
	)
	addParam := func(p map[string]any) {
		pName, _ := p["name"].(string)
		pIn, _ := p["in"].(string)
		if pName == "" || pIn == "" {
			return
		}
		if pIn != "path" && pIn != "query" && pIn != "header" {
			return
		}
		key := pIn + "\x00" + pName
		if idx, ok := paramSeen[key]; ok {
			ordered[idx] = p
			return
		}
		paramSeen[key] = len(ordered)
		ordered = append(ordered, p)
	}
	for _, pVal := range parentParams {
		if p, ok := pVal.(map[string]any); ok {
			addParam(p)
		}
	}
	if opParams, ok := op["parameters"].([]any); ok {
		for _, pVal := range opParams {
			if p, ok := pVal.(map[string]any); ok {
				addParam(p)
			}
		}
	}

	for _, p := range ordered {
		pName, _ := p["name"].(string)
		pIn, _ := p["in"].(string)
		paramLocations[pName] = pIn

		pReq, _ := p["required"].(bool)
		if pIn == "path" {
			pReq = true
		}
		if pReq {
			requiredFields = append(requiredFields, pName)
		}

		pDesc, _ := p["description"].(string)
		var pSchema map[string]any
		if sMap, ok := p["schema"].(map[string]any); ok {
			pSchema = sMap
		} else {
			pSchema = map[string]any{"type": "string"}
		}
		pSchema = inlineSchema(root, pSchema, map[string]bool{})
		if pDesc != "" && pSchema["description"] == nil {
			pSchema["description"] = pDesc
		}
		properties[pName] = pSchema
	}

	// 合并 requestBody
	if reqBody, ok := op["requestBody"].(map[string]any); ok {
		if content, ok := reqBody["content"].(map[string]any); ok {
			var bodySchema map[string]any
			if jsonContent, ok := content["application/json"].(map[string]any); ok {
				if s, ok := jsonContent["schema"].(map[string]any); ok {
					bodySchema = s
				}
			}
			if bodySchema != nil {
				// 递归内联所有 $ref（含嵌套对象/数组），否则 MCP 客户端拿到的是
				// "#/components/schemas/X" 这种无法解析的引用。
				bodySchema = inlineSchema(root, bodySchema, map[string]bool{})

				if bProps, ok := bodySchema["properties"].(map[string]any); ok {
					rename := make(map[string]string, len(bProps))
					for k, v := range bProps {
						if k == "$schema" {
							continue // 忽略 Huma 自动注入的元 schema 字段
						}
						argName := k
						// body 字段与 path/query/header 参数同名时，加前缀避免覆盖。
						if _, exists := properties[argName]; exists {
							argName = "body." + k
							// 用 body:<原名> 记录映射，派发时才知道要写回哪个 JSON key。
							paramLocations[argName] = "body:" + k
						} else {
							paramLocations[argName] = "body"
						}
						rename[k] = argName
						properties[argName] = v
					}
					if bReq, ok := bodySchema["required"].([]any); ok {
						for _, r := range bReq {
							if rStr, ok := r.(string); ok && rStr != "$schema" {
								if renamed, ok := rename[rStr]; ok {
									requiredFields = append(requiredFields, renamed)
								}
							}
						}
					}
				} else {
					// 非 object 请求体（数组/字符串/数字）：若只暴露单个参数，
					// 否则工具会看起来「没有参数」而调用必然 422。
					argName := "body"
					if _, exists := properties[argName]; exists {
						argName = "body.value"
					}
					properties[argName] = bodySchema
					paramLocations[argName] = "body_raw"
					requiredFields = append(requiredFields, argName)
				}
			}
		}
	}

	inputSchema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(requiredFields) > 0 {
		inputSchema["required"] = requiredFields
	}

	return Tool{
		Name:           name,
		Description:    desc,
		Method:         method,
		Path:           path,
		InputSchema:    inputSchema,
		ParamLocations: paramLocations,
	}, nil
}

// inlineSchema 递归内联 OpenAPI 内部的 $ref（JSON Pointer）。
// 遇到循环引用时返回空 schema 并停止，而不是无限递归。
func inlineSchema(root map[string]any, v any, stack map[string]bool) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok && ref != "" {
			if stack[ref] {
				return map[string]any{}
			}
			resolved := resolveJSONPointer(root, ref)
			if resolved == nil {
				return map[string]any{}
			}
			stack[ref] = true
			out := inlineSchema(root, resolved, stack)
			delete(stack, ref)
			// OpenAPI 3.1 允许 $ref 旁边跟其它字段。
			for k, sub := range t {
				if k == "$ref" {
					continue
				}
				out[k] = inlineAny(root, sub, stack)
			}
			return out
		}
		out := make(map[string]any, len(t))
		for k, sub := range t {
			out[k] = inlineAny(root, sub, stack)
		}
		return out
	default:
		return map[string]any{}
	}
}

// inlineAny 与 inlineSchema 相同，但保留任意 JSON 值类型（数组/标量）。
func inlineAny(root map[string]any, v any, stack map[string]bool) any {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok && ref != "" {
			if stack[ref] {
				return map[string]any{}
			}
			resolved := resolveJSONPointer(root, ref)
			if resolved == nil {
				return map[string]any{}
			}
			stack[ref] = true
			out := inlineSchema(root, resolved, stack)
			delete(stack, ref)
			return out
		}
		out := make(map[string]any, len(t))
		for k, sub := range t {
			out[k] = inlineAny(root, sub, stack)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, sub := range t {
			out[i] = inlineAny(root, sub, stack)
		}
		return out
	default:
		return v
	}
}

// resolveJSONPointer 解析 "#/a/b/c" 形式的 JSON Pointer（含 ~0/~1 转义）。
func resolveJSONPointer(root map[string]any, ref string) map[string]any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var current any = root
	for _, part := range parts {
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}
	res, _ := current.(map[string]any)
	return res
}
