package mcpx

import (
	"encoding/json"
	"fmt"
	"regexp"
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

	for pathStr, pathItemVal := range paths {
		pathItem, ok := pathItemVal.(map[string]any)
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

	// 合并 parameters (path, query, header)
	allParams := append([]any(nil), parentParams...)
	if opParams, ok := op["parameters"].([]any); ok {
		allParams = append(allParams, opParams...)
	}

	for _, pVal := range allParams {
		p, ok := pVal.(map[string]any)
		if !ok {
			continue
		}
		pName, _ := p["name"].(string)
		if pName == "" {
			continue
		}
		pIn, _ := p["in"].(string) // "path", "query", etc.
		if pIn != "path" && pIn != "query" {
			continue
		}
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
			pSchema = copyMap(sMap)
		} else {
			pSchema = map[string]any{"type": "string"}
		}
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
				// 支持解引用 $ref: "#/components/schemas/..."
				if refStr, ok := bodySchema["$ref"].(string); ok && refStr != "" {
					if resolved := resolveSchemaRef(root, refStr); resolved != nil {
						bodySchema = resolved
					}
				}

				if bProps, ok := bodySchema["properties"].(map[string]any); ok {
					for k, v := range bProps {
						if k == "$schema" {
							continue // 忽略 Huma 自动注入的元 schema 字段
						}
						properties[k] = v
						paramLocations[k] = "body"
					}
				}
				if bReq, ok := bodySchema["required"].([]any); ok {
					for _, r := range bReq {
						if rStr, ok := r.(string); ok && rStr != "$schema" {
							requiredFields = append(requiredFields, rStr)
						}
					}
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

// resolveSchemaRef 解析 OpenAPI 内部的 JSON Pointer 引用，如 "#/components/schemas/Foo"。
func resolveSchemaRef(root map[string]any, ref string) map[string]any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var current any = root
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}
	res, _ := current.(map[string]any)
	return copyMap(res)
}

func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if subMap, ok := v.(map[string]any); ok {
			out[k] = copyMap(subMap)
		} else {
			out[k] = v
		}
	}
	return out
}
