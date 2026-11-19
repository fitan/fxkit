package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/go-chi/chi/v5"
)

// corsMiddleware 根据 server CORS 配置设置响应头并短路 OPTIONS 预检。
// 空 origins 表示不设置 CORS（非允许全部）。显式 ["*"] 才允许任意 origin。
func corsMiddleware(cfg *Config) func(http.Handler) http.Handler {
	var allowedOrigins []string
	allowCreds := false
	headers := defaultCORSAllowedHeaders
	methods := defaultCORSAllowedMethods
	expose := defaultCORSExposeHeaders
	var maxAge time.Duration
	if cfg != nil {
		allowedOrigins = cfg.CORSAllowedOrigins
		allowCreds = cfg.CORSAllowCredentials
		if len(cfg.CORSAllowedHeaders) > 0 {
			headers = cfg.CORSAllowedHeaders
		}
		if len(cfg.CORSAllowedMethods) > 0 {
			methods = cfg.CORSAllowedMethods
		}
		if len(cfg.CORSExposeHeaders) > 0 {
			expose = cfg.CORSExposeHeaders
		}
		maxAge = cfg.CORSMaxAge
	}

	allowAll := false
	origins := make(map[string]struct{})
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			allowAll = true
			break
		}
		origins[o] = struct{}{}
	}

	allowMethods := strings.Join(methods, ", ")
	allowHeaders := strings.Join(headers, ", ")
	exposeHeaders := strings.Join(expose, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin == "" {
				// 非 CORS 请求（包括普通 OPTIONS）：交给路由决定 404/405/自定义 OPTIONS。
				// 早期实现在这里无条件返回 200，导致任意不存在的 OPTIONS 也被“成功”响应。
				next.ServeHTTP(w, r)
				return
			}

			allowed := false
			if allowAll {
				allowed = true
				if allowCreds {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				}
			} else if _, ok := origins[origin]; ok {
				allowed = true
				w.Header().Set("Access-Control-Allow-Origin", origin)
				if allowCreds {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}
			w.Header().Add("Vary", "Origin")

			if r.Method == http.MethodOptions {
				w.Header().Add("Vary", "Access-Control-Request-Method")
				w.Header().Add("Vary", "Access-Control-Request-Headers")
				if !allowed {
					// 不允许的 Origin 不伪造预检成功；交给路由（通常是 404/405）
					// 或由上层网关返回 403，避免浏览器误以为预检通过。
					next.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Access-Control-Allow-Methods", allowMethods)
				w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
				w.Header().Set("Access-Control-Expose-Headers", exposeHeaders)
				if maxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(maxAge.Seconds())))
				}
				w.WriteHeader(http.StatusOK)
				return
			}

			// 实际请求：只返回浏览器需要的 CORS 响应头，不拦截请求。
			if allowed {
				w.Header().Set("Access-Control-Expose-Headers", exposeHeaders)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// sseWriteDeadlineMiddleware 为 SSE 清除服务器 WriteTimeout，使
// 长连接流不会被 http.Server.WriteTimeout 杀掉。
func sseWriteDeadlineMiddleware(prefixes []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSSERequest(r, prefixes) {
				_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
			}
			next.ServeHTTP(w, r)
		})
	}
}

// otelMiddleware 用 otelhttp 包装每个请求。初始 span 名为 METHOD + 原始路径；
// [routePatternMiddleware] 在路由匹配后改成 METHOD + chi pattern，避免高基数。
// 跳过 Server-Sent-Event 请求，避免 trace context 占满长连接流。
func otelMiddleware(prefixes []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewMiddleware("",
			otelhttp.WithSpanNameFormatter(httpSpanName),
			otelhttp.WithFilter(func(r *http.Request) bool {
				return !isSSERequest(r, prefixes)
			}),
		)(next)
	}
}

// httpSpanName 返回 METHOD + 路由 pattern，优先 chi 路由、其次 r.Pattern，最后原始 URL 路径。
func httpSpanName(_ string, r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if pattern := rc.RoutePattern(); pattern != "" {
			return r.Method + " " + pattern
		}
	}
	if r.Pattern != "" {
		return r.Method + " " + r.Pattern
	}
	return r.Method + " " + r.URL.Path
}

// routePatternMiddleware 将 chi 匹配的路由 pattern 设为 span 名并附加 attribute。
// 须在 [otelMiddleware] 之后运行以确保 span 存在。
func routePatternMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		next.ServeHTTP(w, r)
		if !span.IsRecording() {
			return
		}
		if rc := chi.RouteContext(r.Context()); rc != nil {
			if pattern := rc.RoutePattern(); pattern != "" {
				span.SetName(r.Method + " " + pattern)
				span.SetAttributes(attribute.String("http.route.pattern", pattern))
			}
		}
	})
}

// recoverMiddleware 将 panic 转为 500，记录日志（带 OTel context 以便与活跃 span 关联），并继续处理后续请求。
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				stack := string(debug.Stack())
				slog.ErrorContext(r.Context(), "panic recovered",
					"error", err,
					"stack", stack,
				)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("internal server error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// accessLogMiddleware 输出结构化访问日志。
//
// route 用 chi 路由模板（如 /articles/{id}），避免把真实路径写进日志造成高基数/敏感信息。
func accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		route := ""
		if rc := chi.RouteContext(r.Context()); rc != nil {
			route = rc.RoutePattern()
		}
		if route == "" {
			route = "unmatched"
		}
		slog.InfoContext(r.Context(), "http request",
			"method", r.Method,
			"route", route,
			"status", status,
			"duration_ms", float64(time.Since(start).Microseconds())/1000.0,
			"bytes", rec.bytes,
			"remote_ip", clientIP(r),
		)
	})
}

// responseRecorder 记录状态码与响应字节数（不影响 Flush / Unwrap）。

// requestBodyLogMiddleware 在固定上限内记录 JSON 请求/响应体。
// 高吞吐路由可通过在 chi 子 router 上条件挂载来关闭。自动跳过 GET 与 SSE。
// 跳过 CloudEvents（Dapr pub/sub 应用回调）：体积大、量大，且相对 JSON invoke API 很少需要。
func requestBodyLogMiddleware(ssePrefixes []string) func(http.Handler) http.Handler {
	const maxLoggedBytes = 8 * 1024
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSSERequest(r, ssePrefixes) ||
				r.Method == http.MethodGet ||
				r.Body == nil {
				next.ServeHTTP(w, r)
				return
			}
			ct := r.Header.Get("Content-Type")
			if mediaType, _, err := mime.ParseMediaType(ct); err == nil {
				mt := strings.ToLower(mediaType)
				if strings.Contains(mt, "cloudevents") {
					next.ServeHTTP(w, r)
					return
				}
			}
			if !isJSONContentType(ct) {
				next.ServeHTTP(w, r)
				return
			}

			original := r.Body
			captured, readErr := io.ReadAll(io.LimitReader(original, maxLoggedBytes+1))
			if readErr != nil {
				slog.WarnContext(r.Context(), "request body read failed",
					"method", r.Method,
					"path", r.URL.Path,
					"error", readErr,
				)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			truncated := len(captured) > maxLoggedBytes
			var rest io.Reader = original
			if truncated {
				extra := captured[maxLoggedBytes:]
				captured = captured[:maxLoggedBytes]
				rest = io.MultiReader(bytes.NewReader(extra), original)
			}
			r.Body = &bodyReadCloser{
				Reader: io.MultiReader(bytes.NewReader(captured), rest),
				Closer: original,
			}

			slog.InfoContext(r.Context(), "request body",
				"method", r.Method,
				"path", r.URL.Path,
				"content_type", r.Header.Get("Content-Type"),
				"content_length", r.ContentLength,
				"logged_bytes", len(captured),
				"truncated", truncated,
				"body_preview", redactPreview(bodyPreview(captured)),
			)

			cw := &responseRecorder{ResponseWriter: w, bodyLimit: maxLoggedBytes}
			next.ServeHTTP(cw, r)
			if cw.status == 0 {
				cw.status = http.StatusOK
			}
			slog.InfoContext(r.Context(), "response body",
				"method", r.Method,
				"path", r.URL.Path,
				"status_code", cw.status,
				"content_type", cw.Header().Get("Content-Type"),
				"logged_bytes", cw.body.Len(),
				"truncated", cw.body.Len() >= maxLoggedBytes,
				"body_preview", redactPreview(bodyPreview(cw.body.Bytes())),
			)
		})
	}
}

// bodyReadCloser 把重放用的 Reader 与原始 body 的 Closer 组合成一个请求体。
type bodyReadCloser struct {
	io.Reader
	io.Closer
}

// responseRecorder 同时用于访问日志与 body 日志：
// 始终记录状态码/字节数，bodyLimit > 0 时额外截留响应体。
//
// 早期是两个几乎相同的包装器（statusRecorder / responseBodyCaptureWriter），
// 且同时开启两层日志时会嵌套两层。
type responseRecorder struct {
	http.ResponseWriter
	// status 是首个写入的状态码；尚未写入时为 0（调用方按 200 处理）。
	status int
	// bytes 是已写入响应体的字节数。
	bytes int
	// body 在 bodyLimit > 0 时截留响应体，用于日志预览。
	body bytes.Buffer
	// bodyLimit 是响应体截留上限（字节）；0 表示不截留。
	bodyLimit int
}

// WriteHeader 记录首个状态码并转发给底层 ResponseWriter。
func (w *responseRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write 记录状态码与写入字节数，bodyLimit > 0 时按上限截留响应体。
func (w *responseRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.bodyLimit > 0 && w.body.Len() < w.bodyLimit {
		remain := w.bodyLimit - w.body.Len()
		if remain > len(b) {
			remain = len(b)
		}
		if remain > 0 {
			_, _ = w.body.Write(b[:remain])
		}
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Flush 在底层支持时转发 http.Flusher.Flush。
func (w *responseRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 返回底层 ResponseWriter，供 http.ResponseController 透传。
func (w *responseRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// bodyPreview 返回 body 的文本预览；非 UTF-8 内容返回 <binary:N bytes> 占位。
func bodyPreview(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if utf8.Valid(body) {
		return string(body)
	}
	return fmt.Sprintf("<binary:%d bytes>", len(body))
}

// sensitiveJSONKeys 是请求/响应体日志脱敏时按键名匹配的敏感字段（小写）。
var sensitiveJSONKeys = []string{
	"password", "passwd", "secret", "token", "access_token",
	"refresh_token", "id_token", "authorization", "api_key", "apikey",
	"client_secret", "private_key",
}

// sensitiveValuePattern 匹配 JSON 中的敏感键值对，用于「已被截断、无法结构化解析」
// 的 body：按键掩码，保留其余内容可观测。
var sensitiveValuePattern = func() *regexp.Regexp {
	keys := make([]string, len(sensitiveJSONKeys))
	for i, k := range sensitiveJSONKeys {
		keys[i] = regexp.QuoteMeta(k)
	}
	return regexp.MustCompile(`(?i)"(` + strings.Join(keys, "|") + `)"\s*:\s*("(?:[^"\\]|\\.)*"|\[[^\]]*\]|\{[^{}]*\}|[^,}\]]+)`)
}()

// redactPreview 按键名对 JSON 文本做脱敏。
//
// 结构化路径（完整 JSON）：解析后递归替换敏感键的值。
// 文本路径（被 maxLoggedBytes 截断的 JSON）：用正则按键掩码，
// 而不是「命中关键词就整段丢弃」—— 否则超过 8KB 的响应在日志里完全不可观测。
func redactPreview(s string) string {
	if s == "" {
		return s
	}
	var doc any
	if err := json.Unmarshal([]byte(s), &doc); err == nil {
		if encoded, encErr := json.Marshal(redactValue(doc)); encErr == nil {
			return string(encoded)
		}
		return "<redacted>"
	}

	out := sensitiveValuePattern.ReplaceAllString(s, `"$1":"<redacted>"`)
	if out == s && containsSensitiveKey(strings.ToLower(s)) {
		// 掩码未命中但确实含敏感键（例如键值被截断在中间）：保守丢弃。
		return "<redacted>"
	}
	return out
}

// redactValue 递归替换敏感键的值。
func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if isSensitiveKey(k) {
				out[k] = "<redacted>"
				continue
			}
			out[k] = redactValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = redactValue(item)
		}
		return out
	default:
		return v
	}
}

// isSensitiveKey 判断 JSON 键名是否敏感（忽略大小写与 - / _ 分隔差异）。
func isSensitiveKey(key string) bool {
	norm := strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(key))
	for _, s := range sensitiveJSONKeys {
		if norm == s {
			return true
		}
	}
	return false
}

// containsSensitiveKey 用于非 JSON 文本的兜底判断。
func containsSensitiveKey(lower string) bool {
	for _, s := range sensitiveJSONKeys {
		if strings.Contains(lower, `"`+s+`"`) {
			return true
		}
	}
	return false
}

// isJSONContentType 判断 Content-Type 是否为 application/json 或 +json 后缀。
func isJSONContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// isSSERequest 判定一个请求是否为 SSE。
//
// 两条依据：
//  1. Accept: text/event-stream（客户端显式声明，最可靠）
//  2. 路径命中 [Config.SSEPathPrefixes]（处理不设置 Accept 的客户端）
//
// 路径前缀是显式配置而不是写死的子串：早期用 strings.Contains(path, "/sse/")，
// 既会误伤路径里恰好包含该片段的普通接口，也无法覆盖自定义挂载点。
func isSSERequest(r *http.Request, prefixes []string) bool {
	if r == nil {
		return false
	}
	if r.URL != nil && isSSERequestPath(r.URL.Path, prefixes) {
		return true
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "text/event-stream") {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
}

// isSSERequestPath 判断路径是否命中任一非空的 SSE 前缀。
func isSSERequestPath(path string, prefixes []string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
