package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
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
			origin := r.Header.Get("Origin")
			allowed := false
			if allowAll {
				if allowCreds && origin != "" {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				}
				allowed = true
			} else if origin != "" {
				if _, ok := origins[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
					if allowCreds {
						w.Header().Set("Access-Control-Allow-Credentials", "true")
					}
					allowed = true
				}
			}
			if allowed {
				w.Header().Set("Access-Control-Allow-Methods", allowMethods)
				w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
				w.Header().Set("Access-Control-Expose-Headers", exposeHeaders)
				if maxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(maxAge.Seconds())))
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// sseWriteDeadlineMiddleware clears the server WriteTimeout for SSE so
// long-lived streams are not killed at http.Server.WriteTimeout.
func sseWriteDeadlineMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSSERequest(r) {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		}
		next.ServeHTTP(w, r)
	})
}

// otelMiddleware 用 otelhttp 包装每个请求。初始 span 名为 METHOD + 原始路径；
// [routePatternMiddleware] 在路由匹配后改成 METHOD + chi pattern，避免高基数。
// 跳过 Server-Sent-Event 请求，避免 trace context 占满长连接流。
func otelMiddleware(next http.Handler) http.Handler {
	return otelhttp.NewMiddleware("",
		otelhttp.WithSpanNameFormatter(httpSpanName),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return !isSSERequest(r)
		}),
	)(next)
}

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
		rec := &statusRecorder{ResponseWriter: w}
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

// statusRecorder 记录状态码与响应字节数（不影响 Flush / Unwrap）。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// requestBodyLogMiddleware 在固定上限内记录 JSON 请求/响应体。
// 高吞吐路由可通过在 chi 子 router 上条件挂载来关闭。自动跳过 GET 与 SSE。
// 跳过 CloudEvents（Dapr pub/sub 应用回调）：体积大、量大，且相对 JSON invoke API 很少需要。
func requestBodyLogMiddleware(next http.Handler) http.Handler {
	const maxLoggedBytes = 8 * 1024
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSSERequest(r) ||
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

		cw := &responseBodyCaptureWriter{ResponseWriter: w, limit: maxLoggedBytes}
		next.ServeHTTP(cw, r)
		if cw.statusCode == 0 {
			cw.statusCode = http.StatusOK
		}
		slog.InfoContext(r.Context(), "response body",
			"method", r.Method,
			"path", r.URL.Path,
			"status_code", cw.statusCode,
			"content_type", cw.Header().Get("Content-Type"),
			"logged_bytes", cw.body.Len(),
			"truncated", cw.body.Len() >= maxLoggedBytes,
			"body_preview", redactPreview(bodyPreview(cw.body.Bytes())),
		)
	})
}

type bodyReadCloser struct {
	io.Reader
	io.Closer
}

type responseBodyCaptureWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	limit      int
}

func (w *responseBodyCaptureWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseBodyCaptureWriter) Write(b []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	if w.limit > w.body.Len() {
		remain := w.limit - w.body.Len()
		if remain > len(b) {
			remain = len(b)
		}
		if remain > 0 {
			_, _ = w.body.Write(b[:remain])
		}
	}
	return w.ResponseWriter.Write(b)
}

func (w *responseBodyCaptureWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseBodyCaptureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func bodyPreview(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if utf8.Valid(body) {
		return string(body)
	}
	return fmt.Sprintf("<binary:%d bytes>", len(body))
}

var sensitiveJSONKeys = []string{
	"password", "passwd", "secret", "token", "access_token",
	"refresh_token", "id_token", "authorization", "api_key", "apikey",
	"client_secret", "private_key",
}

// redactPreview 按键名对 JSON 文本做脱敏，而不是「命中关键词就整段丢弃」。
//
// 早期实现只要 body 里出现 "token" 字样就返回 <redacted>，
// 导致大量含 token 字段名的正常业务响应在日志里完全不可观测。
// 这里解析为 JSON、递归替换敏感键的值，解析失败时才退化为整段丢弃。
func redactPreview(s string) string {
	if s == "" {
		return s
	}
	var doc any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		// 非 JSON（或已被截断）：无法结构化脱敏，保守丢弃。
		if containsSensitiveKey(strings.ToLower(s)) {
			return "<redacted>"
		}
		return s
	}
	redacted := redactValue(doc)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return "<redacted>"
	}
	return string(encoded)
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

func isSSERequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.URL != nil && isSSERequestPath(r.URL.Path) {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
}

func isSSERequestPath(path string) bool {
	return strings.Contains(strings.TrimSpace(path), "/sse/")
}
