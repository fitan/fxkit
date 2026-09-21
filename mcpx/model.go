package mcpx

import "github.com/mark3labs/mcp-go/mcp"

// Tool 是从 OpenAPI/Huma 路由导出的统一大模型工具定义。
type Tool struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Method         string            `json:"method"`                   // HTTP 动词 (GET, POST, PUT, DELETE, etc.)
	Path           string            `json:"path"`                     // 原始路由路径 (/articles/{id})
	InputSchema    map[string]any    `json:"inputSchema"`              // 符合 JSON Schema 规范的对象模式
	ParamLocations map[string]string `json:"paramLocations,omitempty"` // 参数位置映射: "path", "query", "body"
}

// ToMCP 将 Tool 转为 github.com/mark3labs/mcp-go/mcp 的标准 Tool 结构。
func (t Tool) ToMCP() mcp.Tool {
	var properties map[string]any
	if p, ok := t.InputSchema["properties"].(map[string]any); ok {
		properties = p
	}
	var required []string
	if r, ok := t.InputSchema["required"].([]string); ok {
		required = r
	} else if rAny, ok := t.InputSchema["required"].([]any); ok {
		for _, v := range rAny {
			if s, ok := v.(string); ok {
				required = append(required, s)
			}
		}
	}
	return mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: properties,
			Required:   required,
		},
	}
}

// OpenAITool 是符合 OpenAI Function Calling 规范的工具格式。
type OpenAITool struct {
	Type     string         `json:"type"` // 固定为 "function"
	Function OpenAIFunction `json:"function"`
}

// OpenAIFunction 是 OpenAI 函数定义。
type OpenAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

// ClaudeTool 是符合 Anthropic Claude Tool Use 规范的工具格式。
type ClaudeTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// GeminiDeclaration 是符合 Google Gemini Function Calling 规范的函数声明。
type GeminiDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

// ToOpenAI 将工具列表转为 OpenAI 规范的工具数组。
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

// ToClaude 将工具列表转为 Anthropic Claude 规范的工具数组。
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

// ToGemini 将工具列表转为 Google Gemini 规范的函数声明数组。
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
