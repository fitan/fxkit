package mcpx

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"gopkg.in/yaml.v3"
)

var invalidIdentRegexp = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

// ParseOpenAPI 从 OpenAPI 3.0 / 3.1 的 YAML 或 JSON 文本中提取 Tool 定义。
func ParseOpenAPI(data []byte) ([]Tool, error) {
	var root map[string]any
	// 优先尝试 JSON，失败后尝试 YAML
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

			tool, err := extractOperation(strings.ToUpper(method), pathStr, op, pathLevelParams)
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

func extractOperation(method, path string, op map[string]any, parentParams []any) (Tool, error) {
	// 1. 获取 Tool 标识名称 (Name)
	name := ""
	if opID, ok := op["operationId"].(string); ok && strings.TrimSpace(opID) != "" {
		name = strings.TrimSpace(opID)
	}
	if name == "" {
		// 自动由 method + path 派生清晰名称
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

	// 3. 构建 InputSchema (JSON Schema object)
	properties := make(map[string]any)
	var requiredFields []string

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
			// 目前优先处理 path 与 query 参数
			continue
		}

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
				// 如果 body 顶层是 object，将其 properties 展开到顶层参数中
				if bProps, ok := bodySchema["properties"].(map[string]any); ok {
					for k, v := range bProps {
						properties[k] = v
					}
				}
				if bReq, ok := bodySchema["required"].([]any); ok {
					for _, r := range bReq {
						if rStr, ok := r.(string); ok {
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
		inputSchema["required"] = deduplicateStrings(requiredFields)
	}

	return Tool{
		Name:        name,
		Description: desc,
		Method:      method,
		Path:        path,
		InputSchema: inputSchema,
	}, nil
}

func copyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func deduplicateStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ToOpenAI 将 Tool 切片转为 OpenAI Function Calling 规范格式。
func ToOpenAI(tools []Tool) []OpenAITool {
	out := make([]OpenAITool, len(tools))
	for i, t := range tools {
		out[i] = OpenAITool{
			Type: "function",
			Function: OpenAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		}
	}
	return out
}

// ToClaude 将 Tool 切片转为 Anthropic Claude Tool Use 规范格式。
func ToClaude(tools []Tool) []ClaudeTool {
	out := make([]ClaudeTool, len(tools))
	for i, t := range tools {
		out[i] = ClaudeTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return out
}

// ToGemini 将 Tool 切片转为 Google Gemini Function Declaration 规范格式。
func ToGemini(tools []Tool) []GeminiDeclaration {
	out := make([]GeminiDeclaration, len(tools))
	for i, t := range tools {
		out[i] = GeminiDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		}
	}
	return out
}
