package mcpx

// Tool 是从 OpenAPI 路由导出的统一大模型工具定义。
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Method      string         `json:"method"`      // HTTP 动词 (GET, POST, PUT, DELETE, etc.)
	Path        string         `json:"path"`        // 原始路由路径 (/articles/{id})
	InputSchema map[string]any `json:"inputSchema"` // 符合 JSON Schema 规范的对象模式
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
