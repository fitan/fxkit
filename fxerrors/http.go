package fxerrors

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

// WriteError 为 err 写入 JSON problem 响应。*Error 使用其 status 序列化；KindInternal
// 与非 *Error 对外固定为 "internal error"，不泄露内部细节（原始错误写入日志）。
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}

	fe := Wrap(err)
	out := *fe // 复制，避免修改调用方持有的 *Error

	if out.Kind == KindInternal {
		// 内部错误必须留痕，否则线上 500 完全无法定位。
		ctx := r.Context()
		slog.ErrorContext(ctx, "http handler error",
			"error", err,
			"kind", string(out.Kind),
			"status", out.Status,
		)
		out.Message = "internal error"
		out.Details = nil
	}

	status := out.Status
	if status < 100 || status > 999 {
		// 非法/零值 Status（例如手写 &Error{Kind: ...} 或从 JSON 反序列化）：
		// 回退到 kind 的默认码，避免 WriteHeader(0) panic。
		status = out.Kind.Status()
		out.Status = status
	}

	if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
		out.TraceID = sc.TraceID().String()
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(&out)
}
