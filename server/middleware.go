package server

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/go-chi/chi/v5"
)

// corsMiddleware 根据 server.cors_allowed_origins 设置 CORS 头并短路 OPTIONS 预检。
// 空列表表示不设置 CORS（非允许全部）。显式 ["*"] 才允许任意 origin。
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
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

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" {
				if _, ok := origins[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			// X-User is intentionally omitted; use Authorization (Bearer) in production.
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// sseWriteDeadlineMiddleware clears the server WriteTimeout for SSE paths so
// long-lived streams are not killed at http.Server.WriteTimeout.
func sseWriteDeadlineMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSSERequestPath(r.URL.Path) {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		}
		next.ServeHTTP(w, r)
	})
}

// otelMiddleware 用 otelhttp 包装每个请求，span 名称为 URL 路径。
// 跳过 Server-Sent-Event 路径，避免 trace context 占满长连接流。
func otelMiddleware(next http.Handler) http.Handler {
	return otelhttp.NewMiddleware("",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.URL.Path
		}),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return !isSSERequestPath(r.URL.Path)
		}),
	)(next)
}

// routePatternMiddleware 将 chi 匹配的路由 pattern 附加到活跃 span。须在 [otelMiddleware] 之后运行以确保 span 存在。
func routePatternMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		next.ServeHTTP(w, r)
		if !span.IsRecording() {
			return
		}
		if rc := chi.RouteContext(r.Context()); rc != nil {
			if pattern := rc.RoutePattern(); pattern != "" {
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

// requestBodyLogMiddleware 在固定上限内记录 JSON 请求/响应体。
// 高吞吐路由可通过在 chi 子 router 上条件挂载来关闭。自动跳过 GET 与 SSE。
// 跳过 CloudEvents（Dapr pub/sub 应用回调）：体积大、量大，且相对 JSON invoke API 很少需要。
func requestBodyLogMiddleware(next http.Handler) http.Handler {
	const maxLoggedBytes = 8 * 1024
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSSERequestPath(r.URL.Path) ||
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
	`"password"`, `"passwd"`, `"secret"`, `"token"`, `"access_token"`,
	`"refresh_token"`, `"authorization"`, `"api_key"`, `"apikey"`,
}

func redactPreview(s string) string {
	if s == "" {
		return s
	}
	low := strings.ToLower(s)
	for _, key := range sensitiveJSONKeys {
		if strings.Contains(low, key) {
			return "<redacted>"
		}
	}
	return s
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

func isSSERequestPath(path string) bool {
	return strings.Contains(strings.TrimSpace(path), "/sse/")
}
