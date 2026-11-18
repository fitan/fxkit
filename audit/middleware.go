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
				slog.WarnContext(r.Context(), "audit sink failed", "error", err)
			}
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

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

func traceID(ctx context.Context) string {
	sc := oteltrace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

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
