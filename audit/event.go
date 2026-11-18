package audit

import (
	"context"
	"time"
)

// Event 是一条审计记录。
type Event struct {
	At        time.Time      `json:"at"`
	Actor     string         `json:"actor,omitempty"`
	OrgID     string         `json:"orgId,omitempty"`
	Method    string         `json:"method"`
	Path      string         `json:"path"`
	Status    int            `json:"status"`
	Action    string         `json:"action,omitempty"`
	Resource  string         `json:"resource,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
	TraceID   string         `json:"traceId,omitempty"`
	IP        string         `json:"ip,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// Sink 是审计事件落点。实现必须并发安全。
type Sink interface {
	Write(ctx context.Context, ev Event) error
}
