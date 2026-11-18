package mcpx_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fitan/fxkit/mcpx"
)

const arrayBodySpec = `
openapi: 3.0.3
info:
  title: t
  version: "1.0"
paths:
  /batch:
    post:
      operationId: batch
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: array
              items:
                type: string
      responses:
        "200":
          description: ok
`

// TestParseOpenAPI_NonObjectBody 回归：数组/标量请求体不能被静默丢弃。
func TestParseOpenAPI_NonObjectBody(t *testing.T) {
	tools, err := mcpx.ParseOpenAPI([]byte(arrayBodySpec))
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools=%d", len(tools))
	}
	tool := tools[0]
	if got := tool.ParamLocations["body"]; got != "body_raw" {
		t.Fatalf("body location=%q, want body_raw", got)
	}

	var gotBody []byte
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("ok"))
	})
	d := mcpx.NewInProcessDispatcher(h, nil)
	if _, err := d.Dispatch(context.Background(), tool, map[string]any{"body": []any{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	var decoded []string
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("body %s is not a JSON array: %v", gotBody, err)
	}
	if len(decoded) != 2 || decoded[0] != "a" || decoded[1] != "b" {
		t.Fatalf("body=%s", gotBody)
	}
}

const collisionSpec = `
openapi: 3.0.3
info:
  title: t
  version: "1.0"
paths:
  /items:
    post:
      operationId: createItem
      parameters:
        - in: query
          name: filter
          schema:
            type: string
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                filter:
                  type: string
                other:
                  type: string
      responses:
        "200":
          description: ok
`

// TestDispatcher_BodyPropertyNameCollision 回归：
// body 字段与 query 参数同名时不能互相覆盖，派发时要各归各位。
func TestDispatcher_BodyPropertyNameCollision(t *testing.T) {
	tools, err := mcpx.ParseOpenAPI([]byte(collisionSpec))
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools=%d", len(tools))
	}
	tool := tools[0]
	if got := tool.ParamLocations["filter"]; got != "query" {
		t.Fatalf("query filter location=%q", got)
	}
	if got := tool.ParamLocations["body.filter"]; got != "body:filter" {
		t.Fatalf("body filter location=%q", got)
	}

	var gotQuery string
	var gotBody []byte
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("ok"))
	})
	d := mcpx.NewInProcessDispatcher(h, nil)
	if _, err := d.Dispatch(context.Background(), tool, map[string]any{
		"filter":      "q",
		"body.filter": "b",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, "filter=q") {
		t.Fatalf("query=%q", gotQuery)
	}
	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	if body["filter"] != "b" {
		t.Fatalf("body=%v", body)
	}
}

const nameCollisionSpec = `
openapi: 3.0.3
info:
  title: t
  version: "1.0"
paths:
  /a-b:
    get:
      responses:
        "200":
          description: ok
  /a_b:
    get:
      responses:
        "200":
          description: ok
`

// TestParseOpenAPI_ToolNamesAreUnique 回归：名称冲突加后缀后必须登记新名字，不能再撞。
func TestParseOpenAPI_ToolNamesAreUnique(t *testing.T) {
	tools, err := mcpx.ParseOpenAPI([]byte(nameCollisionSpec))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool.Name] {
			t.Fatalf("duplicate tool name %q: %+v", tool.Name, tools)
		}
		seen[tool.Name] = true
	}
	if len(seen) != 2 {
		t.Fatalf("tools=%+v", tools)
	}
}
