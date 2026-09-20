package fxerrors

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

// WriteError 为 err 写入 JSON problem 响应。*Error 使用其 status 序列化；KindInternal
// 与非 *Error 对外固定为 "internal error"，不泄露内部细节。
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}
	var fxe *Error
	if errors.As(err, &fxe) {
		out := *fxe
		if out.Kind == KindInternal {
			out.Message = "internal error"
		}
		if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
			out.TraceID = sc.TraceID().String()
		}
		w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
		w.WriteHeader(out.Status)
		_ = json.NewEncoder(w).Encode(&out)
		return
	}
	fe := Wrap(err)
	out := *fe
	if out.Kind == KindInternal {
		out.Message = "internal error"
	}
	if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
		out.TraceID = sc.TraceID().String()
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(out.Status)
	_ = json.NewEncoder(w).Encode(&out)
}
