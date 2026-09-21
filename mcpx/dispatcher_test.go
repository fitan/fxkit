package mcpx_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/fitan/fxkit/mcpx"
	"github.com/go-chi/chi/v5"
)

type dispatchTestInput struct {
	ID    string `path:"id"`
	Limit int    `query:"limit"`
	Body  struct {
		Title string `json:"title" minLength:"3"`
	}
}

type dispatchTestOutput struct {
	Body struct {
		EchoID    string `json:"echo_id"`
		EchoTitle string `json:"echo_title"`
		Limit     int    `json:"limit"`
	}
}

func setupTestHumaMux() (*chi.Mux, huma.API, mcpx.Tool) {
	router := chi.NewMux()
	api := humachi.New(router, huma.DefaultConfig("test-dispatcher", "1.0.0"))

	huma.Register(api, huma.Operation{
		OperationID: "updateArticleItem",
		Method:      http.MethodPost,
		Path:        "/items/{id}",
		Summary:     "Update Item with validation",
	}, func(ctx context.Context, in *dispatchTestInput) (*dispatchTestOutput, error) {
		out := &dispatchTestOutput{}
		out.Body.EchoID = in.ID
		out.Body.EchoTitle = in.Body.Title
		out.Body.Limit = in.Limit
		return out, nil
	})

	tools, _ := mcpx.ParseHuma(api)
	var updateTool mcpx.Tool
	for _, t := range tools {
		if t.Name == "updateArticleItem" {
			updateTool = t
			break
		}
	}

	return router, api, updateTool
}

func TestInProcessDispatcher_Success(t *testing.T) {
	router, _, tool := setupTestHumaMux()
	dispatcher := mcpx.NewInProcessDispatcher(router, map[string]string{
		"Authorization": "Bearer test-token",
	})

	args := map[string]any{
		"id":    "item-456",
		"limit": 50,
		"title": "Clean Code",
	}

	result, err := dispatcher.Dispatch(context.Background(), tool, args)
	if err != nil {
		t.Fatalf("unexpected dispatch error: %v", err)
	}

	if !strings.Contains(result, `"echo_id":"item-456"`) ||
		!strings.Contains(result, `"echo_title":"Clean Code"`) ||
		!strings.Contains(result, `"limit":50`) {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestInProcessDispatcher_ValidationError(t *testing.T) {
	router, _, tool := setupTestHumaMux()
	dispatcher := mcpx.NewInProcessDispatcher(router, nil)

	// 传入不符合 minLength:3 的 title，触发 Huma 原生字段校验
	args := map[string]any{
		"id":    "item-456",
		"title": "a", // length < 3
	}

	_, err := dispatcher.Dispatch(context.Background(), tool, args)
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}

	// 验证错误信息中包含 Huma 的 HTTP 422 状态码与详细字段描述
	if !strings.Contains(err.Error(), "HTTP 422") || !strings.Contains(err.Error(), "validation") {
		t.Fatalf("expected Huma validation error details, got: %v", err)
	}
}
