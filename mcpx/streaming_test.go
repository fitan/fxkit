package mcpx_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/fitan/fxkit/mcpx"
)

// TestInProcessDispatcher_RejectsStreamingHandler 是回归测试：
//
// bufferRecorder.Flush() 早期是空实现，于是绑定到 SSE 端点的 tool 会返回
// 「200 + 空 body」——模型看到一个成功的空结果，而不是错误。
// 现在只要 handler 触发过 Flush，就明确报错。
func TestInProcessDispatcher_RejectsStreamingHandler(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write([]byte("data: hello\n\n"))
	})

	d := mcpx.NewInProcessDispatcher(handler, nil)
	tool := mcpx.Tool{Name: "stream", Method: http.MethodGet, Path: "/sse/stream"}

	_, err := d.Dispatch(context.Background(), tool, map[string]any{})
	if err == nil {
		t.Fatal("dispatching a streaming handler must fail, not return an empty success")
	}
	if !strings.Contains(err.Error(), "streaming") {
		t.Fatalf("error should explain streaming is unsupported, got %v", err)
	}
}
