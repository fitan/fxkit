package audit

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/server"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// maxAuditBodyBytes 是审计记录的请求体上限（8 KiB），超出部分被截断。
const maxAuditBodyBytes = 8 << 10

// Middleware 生成审计事件。默认只记元数据；body/headers 需显式开启。
func Middleware(cfg Config, sink Sink) server.Middleware {
	if !cfg.Enabled || sink == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w}
			body := ""
			if cfg.IncludeBody {
				body = captureRequestBody(r)
			}
			next.ServeHTTP(sw, r)

			ev := Event{
				At:        time.Now().UTC(),
				Method:    r.Method,
				Path:      r.URL.Path,
				Status:    sw.statusCode(),
				RequestID: r.Header.Get("X-Request-Id"),
				TraceID:   traceID(r.Context()),
				IP:        clientIP(r),
			}
			if subj, ok := authz.SubjectFromContext(r.Context()); ok {
				ev.Actor = subj.ID
				ev.OrgID = subj.OrgID
			}
			md := map[string]any{}
			if body != "" {
				md["body"] = body
			}
			if cfg.IncludeHeaders {
				md["headers"] = redactedHeaders(r.Header, cfg.RedactHeaders)
			}
			if len(md) > 0 {
				ev.Metadata = md
			}
			if err := sink.Write(r.Context(), ev); err != nil {
				recordEvent(r.Context(), err)
				slog.WarnContext(r.Context(), "audit sink failed", "error", err)
			} else {
				recordEvent(r.Context(), nil)
			}
		})
	}
}

// statusWriter 包装 http.ResponseWriter 并记录首次写入的状态码。
type statusWriter struct {
	http.ResponseWriter
	// status 是首次写入的状态码；0 表示尚未写入。
	status int
}

// WriteHeader 记录首次写入的状态码并转发给底层 ResponseWriter，后续调用的状态码不再记录。
func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write 在 handler 未显式写状态码时按 200 记录，然后写入响应体。
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Flush 在底层支持 http.Flusher 时转发刷新，否则静默忽略。
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 返回被包装的 ResponseWriter，供 http.ResponseController 等按能力探测。
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// statusCode 返回记录的状态码；未写入过时按 200 处理。
func (w *statusWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// captureRequestBody 读取请求体最多 8 KiB 并放回 r.Body，空体或读取失败时返回空串。
func captureRequestBody(r *http.Request) string {
	if r == nil || r.Body == nil || r.Body == http.NoBody {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxAuditBodyBytes+1))
	if err != nil {
		return ""
	}
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(raw), r.Body))
	if len(raw) > maxAuditBodyBytes {
		raw = raw[:maxAuditBodyBytes]
	}
	return string(raw)
}

// redactedHeaders 把请求头转为 map，命中 redact 列表的头（大小写不敏感）值替换为 [redacted]。
func redactedHeaders(h http.Header, redact []string) map[string]string {
	blocked := make(map[string]bool, len(redact))
	for _, k := range redact {
		blocked[strings.ToLower(strings.TrimSpace(k))] = true
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if blocked[strings.ToLower(k)] {
			out[k] = "[redacted]"
			continue
		}
		out[k] = strings.Join(v, ",")
	}
	return out
}

// traceID 返回当前 span 的 trace ID；无有效 span 时返回空串。
func traceID(ctx context.Context) string {
	sc := oteltrace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// clientIP 取 RemoteAddr 的主机部分；无法解析时原样返回，RemoteAddr 为空时返回 unknown。
func clientIP(r *http.Request) string {
	addr := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	if addr == "" {
		return "unknown"
	}
	return addr
}
