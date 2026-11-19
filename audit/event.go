package audit

import (
	"context"
	"time"
)

// Event 是一条审计记录。
type Event struct {
	// At 事件发生时间（UTC），由中间件在响应结束后写入。
	At time.Time `json:"at"`
	// Actor 认证主体 ID，取自 authz.Subject.ID；无认证信息时为空。
	Actor string `json:"actor,omitempty"`
	// OrgID 主体所属租户/组织，取自 authz.Subject.OrgID。
	OrgID string `json:"orgId,omitempty"`
	// Method 请求的 HTTP 方法。
	Method string `json:"method"`
	// Path 请求路径（r.URL.Path）。
	Path string `json:"path"`
	// Status 响应状态码；handler 未显式写状态时按 200 记录。
	Status int `json:"status"`
	// Action 业务动作名；中间件不填，由业务写入时自定义。
	Action string `json:"action,omitempty"`
	// Resource 被操作资源的标识；中间件不填，由业务自定义。
	Resource string `json:"resource,omitempty"`
	// RequestID 请求 ID，取自 X-Request-Id 请求头。
	RequestID string `json:"requestId,omitempty"`
	// TraceID 当前 OTel span 的 trace ID；无有效 span 时为空。
	TraceID string `json:"traceId,omitempty"`
	// IP 客户端 IP，取 RemoteAddr 的主机部分；RemoteAddr 为空时为 unknown。
	IP string `json:"ip,omitempty"`
	// Metadata 附加信息，如 body、headers；为空时不序列化。
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Sink 是审计事件落点。实现必须并发安全。
type Sink interface {
	// Write 写入一条审计事件；返回错误时中间件记 Warn 并计入 audit.events{result=error}。
	Write(ctx context.Context, ev Event) error
}
