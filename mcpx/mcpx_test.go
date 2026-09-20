package mcpx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fitan/fxkit/mcpx"
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

func TestMCPServer_Protocol_and_ToolCall(t *testing.T) {
	// 搭建 Mock 后端服务
	mux := http.NewServeMux()
	mux.HandleFunc("/articles/100", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"100","title":"Go 1.27 Generics"}`))
			return
		}
		http.Error(w, "bad method", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/articles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "101",
				"title": in["title"],
			})
			return
		}
		http.Error(w, "bad method", http.StatusMethodNotAllowed)
	})

	backendServer := httptest.NewServer(mux)
	defer backendServer.Close()

	tools, err := mcpx.ParseOpenAPI([]byte(sampleOpenAPIYAML))
	if err != nil {
		t.Fatal(err)
	}

	srv := mcpx.NewServer(mcpx.ServerConfig{
		Name:    "test-catalog",
		Version: "1.0.0",
		BaseURL: backendServer.URL,
	}, tools)

	ctx := context.Background()

	// 1. 测试 initialize
	initReq := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	initResp := srv.HandleMessage(ctx, initReq)
	if initResp == nil || initResp.Error != nil {
		t.Fatalf("initialize failed: %+v", initResp)
	}
	resMap, _ := initResp.Result.(map[string]any)
	if resMap["protocolVersion"] != "2024-11-05" {
		t.Fatalf("unexpected protocolVersion: %v", resMap["protocolVersion"])
	}

	// 2. 测试 tools/list
	listReq := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	listResp := srv.HandleMessage(ctx, listReq)
	if listResp == nil || listResp.Error != nil {
		t.Fatalf("tools/list failed: %+v", listResp)
	}

	// 3. 测试 tools/call (GET 带 path 参数)
	callReq := []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"getArticle","arguments":{"id":"100"}}}`)
	callResp := srv.HandleMessage(ctx, callReq)
	if callResp == nil || callResp.Error != nil {
		t.Fatalf("tools/call failed: %+v", callResp)
	}
	callResult, _ := callResp.Result.(map[string]any)
	if callResult["isError"] == true {
		t.Fatalf("tools/call returned error: %+v", callResult)
	}
	contents, _ := callResult["content"].([]map[string]any)
	if len(contents) == 0 || !strings.Contains(contents[0]["text"].(string), "Go 1.27 Generics") {
		t.Fatalf("unexpected tool call result: %+v", contents)
	}

	// 4. 测试 tools/call (POST 带 JSON Body)
	postReq := []byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"createArticle","arguments":{"title":"New Era"}}}`)
	postResp := srv.HandleMessage(ctx, postReq)
	if postResp == nil || postResp.Error != nil {
		t.Fatalf("createArticle call failed: %+v", postResp)
	}
	postResult, _ := postResp.Result.(map[string]any)
	postContents, _ := postResult["content"].([]map[string]any)
	if len(postContents) == 0 || !strings.Contains(postContents[0]["text"].(string), "New Era") {
		t.Fatalf("unexpected post call result: %+v", postContents)
	}

	// 5. 测试 ServeStdio
	var inBuf bytes.Buffer
	inBuf.WriteString(`{"jsonrpc":"2.0","id":5,"method":"ping"}` + "\n")
	var outBuf bytes.Buffer
	if err := srv.ServeStdio(ctx, &inBuf, &outBuf); err != nil {
		t.Fatalf("ServeStdio failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), `"id":5`) {
		t.Fatalf("expected ping response in outBuf, got: %s", outBuf.String())
	}
}
