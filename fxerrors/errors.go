// Package fxerrors 是 fxkit 的统一错误类型。错误序列化为 JSON 供 HTTP handler 使用（见 [WriteError]），
// 字段风格类似 RFC 9457 Problem Details。
//
// 错误模型（对应 RFC 9457 Problem Details）：
//
//	{
//	  "code":    "not_found",
//	  "status":  404,
//	  "message": "user not found (id=42)",
//	  "details": {"id": 42},
//	  "trace_id": "..."   // OTel span 活跃时自动填充
//	}
//
// 使用 [Wrap] 包装第三方错误，在 fxkit 日志/中间件栈中保留 trace，且不丢失底层 cause：
//
//	if err := repo.Save(ctx, u); err != nil {
//	    return nil, fxerrors.Wrap(err)
//	}
//
// 使用 [Is] / [errors.As] 匹配 kind：
//
//	if fxerrors.Is(err, fxerrors.KindNotFound) { ... }
package fxerrors

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

// Kind 是机器可读的错误类别。新增 kind 应在 [Kind.Status] 中映射到稳定 HTTP 状态码。
type Kind string

const (
	KindUnknown          Kind = "unknown"
	KindBadRequest       Kind = "bad_request"
	KindValidation       Kind = "validation"
	KindUnauthorized     Kind = "unauthorized"
	KindPermissionDenied Kind = "permission_denied"
	KindNotFound         Kind = "not_found"
	KindConflict         Kind = "conflict"
	KindUnprocessable    Kind = "unprocessable"
	KindTooManyRequests  Kind = "too_many_requests"
	KindInternal         Kind = "internal"
	KindUnavailable      Kind = "unavailable"
	KindTimeout          Kind = "timeout"
)

// Status 返回 kind 对应的 HTTP 状态码。未知 kind 回退为 500。
func (k Kind) Status() int {
	switch k {
	case KindBadRequest, KindValidation:
		return http.StatusBadRequest
	case KindUnauthorized:
		return http.StatusUnauthorized
	case KindPermissionDenied:
		return http.StatusForbidden
	case KindNotFound:
		return http.StatusNotFound
	case KindConflict:
		return http.StatusConflict
	case KindUnprocessable:
		return http.StatusUnprocessableEntity
	case KindTooManyRequests:
		return http.StatusTooManyRequests
	case KindUnavailable:
		return http.StatusServiceUnavailable
	case KindTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

// Error 是 fxkit 标准错误。请使用下方辅助构造函数（[NotFound]、[Conflict] 等），
// 勿直接构造，以保持 kind/status 映射一致。
type Error struct {
	Kind    Kind           `json:"code"`
	Status  int            `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	TraceID string         `json:"trace_id,omitempty"`

	cause error
}

// Error 满足 error 接口。
func (e *Error) Error() string { return e.Message }

// GetStatus 返回此错误的 HTTP 状态码。
func (e *Error) GetStatus() int { return e.Status }

// Unwrap 向 [errors.Is] / [errors.As] 暴露底层 cause。
func (e *Error) Unwrap() error { return e.cause }

// ContentType 为 JSON 错误响应选择 Content-Type。
func (e *Error) ContentType(ct string) string {
	if ct == "application/json" {
		return "application/problem+json"
	}
	return ct
}

// WithDetails 附加任意键值对。可链式调用：
//
//	fxerrors.Conflict("email exists").WithDetails(map[string]any{"email": email})
func (e *Error) WithDetails(d map[string]any) *Error {
	if e == nil {
		return nil
	}
	out := *e
	out.Details = map[string]any{}
	for k, v := range e.Details {
		out.Details[k] = v
	}
	for k, v := range d {
		out.Details[k] = v
	}
	return &out
}

// WithContext 将活跃 OTel trace id 写入错误，使其与 slog 的 `trace_id` 属性一并出现在响应体中。
func (e *Error) WithContext(ctx context.Context) *Error {
	if e == nil {
		return nil
	}
	out := *e
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		out.TraceID = sc.TraceID().String()
	}
	return &out
}

// new_ 是下方公开辅助函数使用的唯一构造函数。
func new_(kind Kind, format string, args ...any) *Error {
	return &Error{
		Kind:    kind,
		Status:  kind.Status(),
		Message: fmt.Sprintf(format, args...),
	}
}

// BadRequest 表示格式错误或不符合约定的请求（HTTP 400）。
func BadRequest(format string, args ...any) *Error { return new_(KindBadRequest, format, args...) }

// Validation 表示一个或多个字段级校验失败（HTTP 400）。
// 将 field/message map 作为 Details —— 客户端可在表单 UI 中展示。
func Validation(fields map[string]string) *Error {
	e := new_(KindValidation, "validation failed")
	if len(fields) > 0 {
		d := make(map[string]any, len(fields))
		for k, v := range fields {
			d[k] = v
		}
		e.Details = d
	}
	return e
}

// Unauthorized 表示缺失或无效凭证（HTTP 401）。
func Unauthorized(format string, args ...any) *Error { return new_(KindUnauthorized, format, args...) }

// PermissionDenied 表示已认证用户缺少所需授权（HTTP 403）。
func PermissionDenied(format string, args ...any) *Error {
	return new_(KindPermissionDenied, format, args...)
}

// NotFound 表示指定资源不存在（HTTP 404）。
//
//	fxerrors.NotFound("user", "id=%d", id)
//	fxerrors.NotFound("topic", "%s/%s", pubsub, topic)
func NotFound(resource string, format string, args ...any) *Error {
	msg := resource + " not found"
	if format != "" {
		msg += " (" + fmt.Sprintf(format, args...) + ")"
	}
	e := new_(KindNotFound, "%s", msg)
	e.Details = map[string]any{"resource": resource}
	return e
}

// Conflict 表示唯一性或状态冲突（HTTP 409）。
func Conflict(format string, args ...any) *Error { return new_(KindConflict, format, args...) }

// Unprocessable 表示语义无效但已通过语法校验的输入（HTTP 422）。
func Unprocessable(format string, args ...any) *Error {
	return new_(KindUnprocessable, format, args...)
}

// TooManyRequests 表示限流（HTTP 429）。
func TooManyRequests(format string, args ...any) *Error {
	return new_(KindTooManyRequests, format, args...)
}

// Internal 表示意外的服务端故障（HTTP 500）。向上传递第三方错误时优先 [Wrap]。
func Internal(format string, args ...any) *Error { return new_(KindInternal, format, args...) }

// Unavailable 表示服务当前无法处理请求（HTTP 503）。
func Unavailable(format string, args ...any) *Error { return new_(KindUnavailable, format, args...) }

// Timeout 表示下游调用超过 deadline（HTTP 504）。
func Timeout(format string, args ...any) *Error { return new_(KindTimeout, format, args...) }

// Wrap 将任意 error 转为 fxkit Error。若已是 *Error 则原样返回，避免多层包装降低 kind/status。
// 否则结果为 KindInternal，消息为原 error 字符串，并保留 cause。
func Wrap(err error) *Error {
	if err == nil {
		return nil
	}
	var fxe *Error
	if errors.As(err, &fxe) {
		return fxe
	}
	return &Error{
		Kind:    KindInternal,
		Status:  KindInternal.Status(),
		Message: "internal error",
		cause:   err,
	}
}

// Is 报告 err（或其包装 cause）是否为给定 kind 的 fxkit Error。便于一行模式匹配：
//
//	if fxerrors.Is(err, fxerrors.KindNotFound) { return defaultValue }
func Is(err error, kind Kind) bool {
	var fxe *Error
	if !errors.As(err, &fxe) {
		return false
	}
	return fxe.Kind == kind
}
