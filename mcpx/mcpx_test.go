package mcpx_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/fitan/fxkit/mcpx"
	"github.com/go-chi/chi/v5"
	"github.com/mark3labs/mcp-go/mcp"
)

const sampleOpenAPIYAML = `
openapi: 3.0.3
info:
  title: Catalog Service
  version: 1.0.0
paths:
  /articles:
    get:
      operationId: listArticles
      summary: List all articles with optional filtering
      parameters:
        - name: q
          in: query
          schema:
            type: string
          description: Search expression
      responses:
        "200":
          description: Success
    post:
      operationId: createArticle
      summary: Create a new article
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required:
                - title
              properties:
                title:
                  type: string
                content:
                  type: string
      responses:
        "201":
          description: Created
  /articles/{id}:
    get:
      operationId: getArticle
      summary: Get article by ID
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: Success
`

func TestParseOpenAPI_and_LLMAdapters(t *testing.T) {
	tools, err := mcpx.ParseOpenAPI([]byte(sampleOpenAPIYAML))
	if err != nil {
		t.Fatalf("ParseOpenAPI failed: %v", err)
	}

	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	toolMap := make(map[string]mcpx.Tool)
	for _, tool := range tools {
		toolMap[tool.Name] = tool
	}

	// 1. 验证 getArticle
	getArt, ok := toolMap["getArticle"]
	if !ok {
		t.Fatalf("getArticle tool not found")
	}
	if getArt.Method != "GET" || getArt.Path != "/articles/{id}" {
		t.Fatalf("unexpected getArticle method/path: %s %s", getArt.Method, getArt.Path)
	}
	props, _ := getArt.InputSchema["properties"].(map[string]any)
	if props["id"] == nil {
		t.Fatalf("expected id in properties of getArticle")
	}

	// 2. 验证 createArticle
	createArt, ok := toolMap["createArticle"]
	if !ok {
		t.Fatalf("createArticle tool not found")
	}
	reqFields, _ := createArt.InputSchema["required"].([]string)
	if len(reqFields) == 0 || reqFields[0] != "title" {
		t.Fatalf("expected title required in createArticle, got %v", reqFields)
	}

	// 3. 验证 OpenAI Adapter
	openaiTools := mcpx.ToOpenAI(tools)
	if len(openaiTools) != 3 || openaiTools[0].Type != "function" {
		t.Fatalf("invalid openai tools: %+v", openaiTools)
	}

	// 4. 验证 Claude Adapter
	claudeTools := mcpx.ToClaude(tools)
	if len(claudeTools) != 3 || claudeTools[0].InputSchema == nil {
		t.Fatalf("invalid claude tools: %+v", claudeTools)
	}

	// 5. 验证 Gemini Adapter
	geminiTools := mcpx.ToGemini(tools)
	if len(geminiTools) != 3 || geminiTools[0].Parameters == nil {
		t.Fatalf("invalid gemini tools: %+v", geminiTools)
	}
}

type updateArticleInput struct {
	ID   string `path:"id"`
	Body struct {
		Title string `json:"title"`
	}
}

type updateArticleOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

func TestMCPServer_InMemoryHuma_DirectDispatch(t *testing.T) {
	// 1. 搭建原生的 Huma 路由 (chi.Mux)
	router := chi.NewMux()
	api := humachi.New(router, huma.DefaultConfig("in-memory-catalog", "1.0.0"))

	huma.Register(api, huma.Operation{
		OperationID: "updateArticleInMemory",
		Method:      http.MethodPost,
		Path:        "/articles/{id}",
		Summary:     "Update article in memory directly",
	}, func(ctx context.Context, input *updateArticleInput) (*updateArticleOutput, error) {
		out := &updateArticleOutput{}
		out.Body.Message = fmt.Sprintf("Success: id=%s title=%s", input.ID, input.Body.Title)
		return out, nil
	})

	// 2. 从已注册的 Huma API 中提取工具
	tools, err := mcpx.ParseHuma(api)
	if err != nil {
		t.Fatalf("ParseHuma failed: %v", err)
	}

	// 3. 重点：创建 MCP Server 并直接注入 router 作为 Handler (0 网络调用，完全内存直通)
	srv := mcpx.NewServer(mcpx.ServerConfig{
		Name:    "in-memory-mcp",
		Handler: router,
	}, tools)

	// 4. 触发 tools/call
	mcpServer := srv.MCPServer()
	tool := mcpServer.GetTool("updateArticleInMemory")
	if tool == nil {
		t.Fatalf("tool updateArticleInMemory not registered")
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = "updateArticleInMemory"
	req.Params.Arguments = map[string]any{
		"id":    "888",
		"title": "Zero Network Overhead",
	}

	res, err := tool.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("tool execution error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res)
	}
	if len(res.Content) == 0 {
		t.Fatalf("empty result content")
	}
	text := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "Success: id=888 title=Zero Network Overhead") {
		t.Fatalf("unexpected dispatch result: %s", text)
	}
}

func TestMCPServer_mark3labs_Integration(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/articles/100", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"100","title":"Go 1.27 Generics"}`))
			return
		}
		http.Error(w, "bad method", http.StatusMethodNotAllowed)
	})

	tools, err := mcpx.ParseOpenAPI([]byte(sampleOpenAPIYAML))
	if err != nil {
		t.Fatal(err)
	}

	// 纯内存直通注入 mux
	srv := mcpx.NewServer(mcpx.ServerConfig{
		Name:    "test-catalog",
		Version: "1.0.0",
		Handler: mux,
	}, tools)

	mcpServer := srv.MCPServer()
	registeredTools := mcpServer.ListTools()
	if len(registeredTools) != 3 {
		t.Fatalf("expected 3 tools in mcp-go server, got %d", len(registeredTools))
	}

	ctx := context.Background()

	getTool := mcpServer.GetTool("getArticle")
	if getTool == nil {
		t.Fatalf("getArticle not registered on MCPServer")
	}

	getReq := mcp.CallToolRequest{}
	getReq.Params.Name = "getArticle"
	getReq.Params.Arguments = map[string]any{"id": "100"}

	getRes, err := getTool.Handler(ctx, getReq)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("handler returned tool error: %+v", getRes)
	}
	textContent := getRes.Content[0].(mcp.TextContent)
	if !strings.Contains(textContent.Text, "Go 1.27 Generics") {
		t.Fatalf("unexpected text content: %+v", getRes.Content[0])
	}
}
