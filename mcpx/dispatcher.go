package mcpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// bufferRecorder 是可完全重置的 ResponseWriter（httptest.ResponseRecorder 的
// wroteHeader 未导出，复用时会忽略第二次 WriteHeader）。
type bufferRecorder struct {
	// header 是响应头，首次调用 Header 时惰性创建。
	header http.Header
	// code 是首个写入的状态码；未写入时为 0（按 200 处理）。
	code int
	// buf 缓存响应体。
	buf bytes.Buffer
	// flushed 标记是否发生过 Flush，用于识别流式（SSE）端点。
	flushed bool
}

// Header 返回响应头集合，首次调用时惰性创建。
func (r *bufferRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

// WriteHeader 只记录首个状态码。
func (r *bufferRecorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
}

// Write 把响应体写入内部缓冲区，未写状态码时按 200 处理。
func (r *bufferRecorder) Write(p []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.buf.Write(p)
}

// Flush 满足 http.Flusher。
//
// 只记标记、不丢数据：tool 调用结束后若发现发生过 Flush，
// 说明这是流式（SSE）端点，无法作为一个 MCP tool 的同步结果返回。
// 早期这里是空实现，结果是「看起来成功、内容是空的」。
func (r *bufferRecorder) Flush() { r.flushed = true }

// reset 清空状态码、Flush 标记、缓冲区与响应头，供池化复用。
func (r *bufferRecorder) reset() {
	r.code = 0
	r.flushed = false
	r.buf.Reset()
	for k := range r.header {
		delete(r.header, k)
	}
}

// recorderPool 复用 ResponseWriter，避免每次 tool 调用分配（进程内直通是高频路径）。
var recorderPool = sync.Pool{New: func() any { return &bufferRecorder{} }}

// InProcessDispatcher 是进程内直通执行器。
// 它完全绕过外部网络 IO 和监听端口，直接构造内存级 HTTP 请求投递给当前微服务的 http.Handler (即 chi.Mux / Huma)。
// 原生触发 Huma 完整的 Path/Query/Body 反序列化与严格字段校验规则。
type InProcessDispatcher struct {
	// handler 是进程内直通的 HTTP handler（通常是 chi.Mux）。
	handler http.Handler
	// headers 是转发时附带的固定请求头。
	headers map[string]string
}

// NewInProcessDispatcher 创建一个内存直通执行器。
func NewInProcessDispatcher(handler http.Handler, headers map[string]string) *InProcessDispatcher {
	return &InProcessDispatcher{
		handler: handler,
		headers: headers,
	}
}

// Dispatch 在内存中执行 tool 对应的 HTTP 请求并返回响应体。
// 流式响应（发生过 Flush）或非 2xx 响应会返回错误。
func (d *InProcessDispatcher) Dispatch(ctx context.Context, tool Tool, args map[string]any) (string, error) {
	if d.handler == nil {
		return "", fmt.Errorf("in-process handler is not set")
	}

	req, err := buildHTTPRequest(ctx, tool, d.headers, args)
	if err != nil {
		return "", err
	}

	// 纯内存执行，0 网络开销
	rec := recorderPool.Get().(*bufferRecorder)
	rec.reset()
	d.handler.ServeHTTP(rec, req)

	respBody := rec.buf.String()
	status := rec.code
	flushed := rec.flushed
	if status == 0 {
		status = http.StatusOK
	}
	rec.reset()
	recorderPool.Put(rec)

	if flushed {
		return "", fmt.Errorf("tool %q targets a streaming endpoint; streaming responses cannot be returned as a synchronous MCP tool result", tool.Name)
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("HTTP %d error: %s", status, strings.TrimSpace(respBody))
	}

	return respBody, nil
}

// buildHTTPRequest 根据 Tool 定义和参数组装内存 HTTP 请求 (精确识别 Path 变量占位符、Query 参数拼接和 JSON Body)。
func buildHTTPRequest(ctx context.Context, tool Tool, headers map[string]string, args map[string]any) (*http.Request, error) {
	targetPath := tool.Path
	queryParams := make(map[string]any)
	bodyParams := make(map[string]any)
	headerParams := make(map[string]string)
	var rawBody any
	hasRawBody := false

	isGetOrDelete := strings.EqualFold(tool.Method, http.MethodGet) || strings.EqualFold(tool.Method, http.MethodDelete)

	for k, v := range args {
		loc := ""
		if tool.ParamLocations != nil {
			loc = tool.ParamLocations[k]
		}

		placeholder := fmt.Sprintf("{%s}", k)
		switch {
		case loc == "path" || strings.Contains(targetPath, placeholder):
			// path 参数必须转义：否则 id="a/b" 会命中另一个路由，
			// id="x?admin=1" 会注入 query。
			targetPath = strings.ReplaceAll(targetPath, placeholder, url.PathEscape(fmt.Sprintf("%v", v)))
		case loc == "body_raw":
			// 非 object 请求体：整个值直接作为 body 序列化。
			rawBody = v
			hasRawBody = true
		case loc == "body":
			bodyParams[k] = v
		case strings.HasPrefix(loc, "body:"):
			// body 字段与参数同名时生成的别名，写回原始 JSON key。
			bodyParams[strings.TrimPrefix(loc, "body:")] = v
		case loc == "header":
			headerParams[k] = fmt.Sprintf("%v", v)
		case loc == "query" || isGetOrDelete:
			queryParams[k] = v
		default:
			bodyParams[k] = v
		}
	}

	if strings.Contains(targetPath, "{") {
		return nil, fmt.Errorf("missing path parameter while building %s %s", tool.Method, tool.Path)
	}

	fullURL := targetPath

	// 组装 Query 参数
	if len(queryParams) > 0 {
		parsedURL, err := url.Parse(fullURL)
		if err == nil {
			q := parsedURL.Query()
			for k, v := range queryParams {
				if slice, ok := v.([]any); ok {
					for _, item := range slice {
						q.Add(k, fmt.Sprintf("%v", item))
					}
				} else {
					q.Set(k, fmt.Sprintf("%v", v))
				}
			}
			parsedURL.RawQuery = q.Encode()
			fullURL = parsedURL.String()
		}
	}

	// 组装 Body
	var bodyReader io.Reader
	if hasRawBody {
		b, err := json.Marshal(rawBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	} else if len(bodyParams) > 0 {
		b, err := json.Marshal(bodyParams)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	// 构造内存 Request
	req, err := http.NewRequestWithContext(ctx, tool.Method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Internal-MCP", "true")

	for k, v := range headerParams {
		req.Header.Set(k, v)
	}
	// 静态 header（服务级凭据）先写；调用者身份最后写，确保身份不被静态值覆盖。
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, vals := range identityHeadersFromContext(ctx) {
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}

	return req, nil
}
