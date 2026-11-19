package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/outbox"
)

// SlogSink 把审计事件写入结构化日志。
type SlogSink struct{}

// Write 把事件元数据写入一条 Info 级别的 slog 日志，始终返回 nil。
func (SlogSink) Write(ctx context.Context, ev Event) error {
	slog.InfoContext(ctx, "audit",
		"actor", ev.Actor, "org", ev.OrgID, "method", ev.Method,
		"path", ev.Path, "status", ev.Status, "request_id", ev.RequestID,
		"trace_id", ev.TraceID, "ip", ev.IP,
	)
	return nil
}

// EventRecord 是 audit_events 表的行。
type EventRecord struct {
	// ID 自增主键。
	ID uint64 `gorm:"primaryKey"`
	// At 事件时间，带索引便于按时间检索。
	At time.Time `gorm:"index"`
	// Actor 认证主体 ID，长度 128，带索引。
	Actor string `gorm:"size:128;index"`
	// OrgID 租户/组织 ID，长度 128，带索引。
	OrgID string `gorm:"size:128;index"`
	// Method HTTP 方法，长度 16。
	Method string `gorm:"size:16"`
	// Path 请求路径，长度 512。
	Path string `gorm:"size:512"`
	// Status 响应状态码。
	Status int
	// Action 业务动作名，长度 64。
	Action string `gorm:"size:64"`
	// Resource 资源标识，长度 128。
	Resource string `gorm:"size:128"`
	// RequestID 请求 ID，长度 64。
	RequestID string `gorm:"size:64"`
	// TraceID 链路 trace ID，长度 64。
	TraceID string `gorm:"size:64"`
	// IP 客户端 IP，长度 64。
	IP string `gorm:"size:64"`
	// Metadata 附加信息的 JSON 字符串（由 [Event.Metadata] 序列化），text 列。
	Metadata string `gorm:"type:text"`
}

// TableName 返回固定表名 audit_events。
func (EventRecord) TableName() string { return "audit_events" }

// DBSink 把审计事件写入数据库。表由 DBSink.Migrate 或外部 DDL 创建。
type DBSink struct {
	// client 是写入 audit_events 的 gormx 客户端；未配置时各方法返回 Internal 错误。
	client *gormx.Client
}

// NewDBSink 用给定 gormx.Client 构造 DBSink。
func NewDBSink(client *gormx.Client) *DBSink { return &DBSink{client: client} }

// Migrate 对 audit_events 表执行 AutoMigrate；client 未配置时返回 Internal 错误。
func (s *DBSink) Migrate(ctx context.Context) error {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return fxerrors.Internal("audit: db sink requires a configured database")
	}
	return s.client.Conn(ctx).AutoMigrate(&EventRecord{})
}

// Write 把事件（Metadata 序列化为 JSON）写入 audit_events；client 未配置时返回 Internal 错误。
func (s *DBSink) Write(ctx context.Context, ev Event) error {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return fxerrors.Internal("audit: db sink requires a configured database")
	}
	md := ""
	if len(ev.Metadata) > 0 {
		b, err := json.Marshal(ev.Metadata)
		if err != nil {
			return fxerrors.Wrap(err)
		}
		md = string(b)
	}
	row := EventRecord{
		At: ev.At, Actor: ev.Actor, OrgID: ev.OrgID, Method: ev.Method,
		Path: ev.Path, Status: ev.Status, Action: ev.Action, Resource: ev.Resource,
		RequestID: ev.RequestID, TraceID: ev.TraceID, IP: ev.IP, Metadata: md,
	}
	return s.client.Conn(ctx).Create(&row).Error
}

// OutboxSink 把审计事件写入 outbox topic，由下游异步落库。
type OutboxSink struct {
	// store 是写入消息的 outbox 存储；为空时 Write 返回 Internal 错误。
	store *outbox.Store
	// topic 是消息写入的 outbox topic，取自配置 audit.topic（默认 audit-event）。
	topic string
}

// NewOutboxSink 用给定 outbox.Store 与 topic 构造 OutboxSink。
func NewOutboxSink(store *outbox.Store, topic string) *OutboxSink {
	return &OutboxSink{store: store, topic: topic}
}

// Write 把事件作为消息写入配置的 outbox topic；store 为空时返回 Internal 错误。
func (s *OutboxSink) Write(ctx context.Context, ev Event) error {
	if s == nil || s.store == nil {
		return fxerrors.Internal("audit: outbox sink requires an outbox store")
	}
	return outbox.EnqueueTopicMsg(ctx, outbox.EnqueueTopicInput[Event]{
		Store: s.store,
		Topic: s.topic,
		Msg:   ev,
	})
}
