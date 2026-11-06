package fxerrors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Kind 表示错误的领域分类，决定默认的 HTTP 状态码。
type Kind string

const (
	KindBadRequest       Kind = "bad_request"
	KindUnauthorized     Kind = "unauthorized"
	KindPermissionDenied Kind = "permission_denied"
	KindNotFound         Kind = "not_found"
	KindConflict         Kind = "conflict"
	KindValidation       Kind = "validation"
	KindUnprocessable    Kind = "unprocessable"
	KindTooManyRequests  Kind = "too_many_requests"
	KindInternal         Kind = "internal"
	KindUnavailable      Kind = "unavailable"
	KindTimeout          Kind = "timeout"
	KindCanceled         Kind = "canceled"
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
	case KindCanceled:
		// 499 不是标准状态码（nginx 惯例）；客户端已断开时通常不会读到响应。
		return 499
	default:
		return http.StatusInternalServerError
	}
}

// Error 是 fxkit 标准错误。请使用下方辅助构造函数（[NotFound]、[Conflict] 等），
// 勿直接构造，以保持 kind/status 映射一致。
type Error struct {
	Kind    Kind           `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	TraceID string         `json:"trace_id,omitempty"`

	cause error
}

// MarshalJSON 把 status 作为派生字段输出（由 Kind 决定）。
//
// 早期 Status 是结构体上的可写字段，于是必须到处防「手写 &Error{} 导致的零值」，
// 而且 code 与 status 可能不一致。现在只有 Kind 是真源，status 在序列化时计算。
func (e *Error) MarshalJSON() ([]byte, error) {
	type wire struct {
		Kind    Kind           `json:"code"`
		Status  int            `json:"status"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
		TraceID string         `json:"trace_id,omitempty"`
	}
	if e == nil {
		return []byte("null"), nil
	}
	return json.Marshal(wire{
		Kind:    e.Kind,
		Status:  e.Kind.Status(),
		Message: e.Message,
		Details: e.Details,
		TraceID: e.TraceID,
	})
}

// Error 满足 error 接口。Message 为空时回退到 cause / kind。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	return string(e.Kind)
}

// Unwrap 向 [errors.Is] / [errors.As] 暴露底层 cause。
func (e *Error) Unwrap() error { return e.cause }

// WithCause 附加底层错误，保留 [errors.Is] / [errors.As] 链。
//
//	fxerrors.Conflict("email exists").WithCause(gorm.ErrDuplicatedKey)
func (e *Error) WithCause(cause error) *Error {
	if e == nil {
		return nil
	}
	out := *e
	out.cause = cause
	return &out
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

func new_(k Kind, format string, args ...any) *Error {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	return &Error{
		Kind:    k,
		Message: msg,
	}
}

// BadRequest 表示客户端输入在语法上有效但语义上有误（HTTP 400）。
func BadRequest(format string, args ...any) *Error { return new_(KindBadRequest, format, args...) }

// Unauthorized 表示缺少认证或认证无效（HTTP 401）。
func Unauthorized(format string, args ...any) *Error {
	return new_(KindUnauthorized, format, args...)
}

// PermissionDenied 表示认证主体无权执行此操作（HTTP 403）。
func PermissionDenied(format string, args ...any) *Error {
	return new_(KindPermissionDenied, format, args...)
}

// NotFound 表示指定资源不存在（HTTP 404）。resource 形如 "user"、"team"。
func NotFound(resource string, format string, args ...any) *Error {
	msg := fmt.Sprintf("%s not found", resource)
	if format != "" {
		detail := format
		if len(args) > 0 {
			detail = fmt.Sprintf(format, args...)
		}
		msg = fmt.Sprintf("%s not found: %s", resource, detail)
	}
	return &Error{
		Kind:    KindNotFound,
		Message: msg,
		Details: map[string]any{"resource": resource},
	}
}

// Conflict 表示因状态冲突而失败，如唯一约束冲突（HTTP 409）。
func Conflict(format string, args ...any) *Error { return new_(KindConflict, format, args...) }

// Validation 表示一个或多个字段级校验失败（HTTP 400）。
// 将 field/message map 作为 Details —— 客户端可在表单 UI 中展示。
//
//	fxerrors.Validation(map[string]string{"email": "invalid format"})
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

// Unprocessable 表示语义正确但业务规则拒绝（HTTP 422）。
func Unprocessable(format string, args ...any) *Error {
	return new_(KindUnprocessable, format, args...)
}

// TooManyRequests 表示超出限流配额（HTTP 429）。
func TooManyRequests(format string, args ...any) *Error {
	return new_(KindTooManyRequests, format, args...)
}

// Internal 表示未预期的系统内部错误（HTTP 500）。
// 敏感信息应放入日志，Message 会在生产环境对外屏蔽。
func Internal(format string, args ...any) *Error { return new_(KindInternal, format, args...) }

// Unavailable 表示服务当前无法处理请求（HTTP 503）。
func Unavailable(format string, args ...any) *Error { return new_(KindUnavailable, format, args...) }

// Timeout 表示下游调用超过 deadline（HTTP 504）。
func Timeout(format string, args ...any) *Error { return new_(KindTimeout, format, args...) }

// Wrap 将任意 error 转为 fxkit Error。若已是 *Error 则原样返回，避免多层包装降低 kind/status。
// 否则结果为 KindInternal，消息为原 error 字符串，并保留 cause。
//
// context 的取消/超时会被映射为对应的 kind，避免客户端断连被当成 500 污染 SLO。
func Wrap(err error) *Error {
	if err == nil {
		return nil
	}
	var fxe *Error
	if errors.As(err, &fxe) {
		return fxe
	}
	switch {
	case errors.Is(err, context.Canceled):
		return &Error{Kind: KindCanceled, Message: "request canceled", cause: err}
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Kind: KindTimeout, Message: "deadline exceeded", cause: err}
	}
	return &Error{
		Kind:    KindInternal,
		Message: err.Error(),
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
