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
	ID        uint64    `gorm:"primaryKey"`
	At        time.Time `gorm:"index"`
	Actor     string    `gorm:"size:128;index"`
	OrgID     string    `gorm:"size:128;index"`
	Method    string    `gorm:"size:16"`
	Path      string    `gorm:"size:512"`
	Status    int
	Action    string `gorm:"size:64"`
	Resource  string `gorm:"size:128"`
	RequestID string `gorm:"size:64"`
	TraceID   string `gorm:"size:64"`
	IP        string `gorm:"size:64"`
	Metadata  string `gorm:"type:text"`
}

func (EventRecord) TableName() string { return "audit_events" }

// DBSink 把审计事件写入数据库。表由 DBSink.Migrate 或外部 DDL 创建。
type DBSink struct{ client *gormx.Client }

func NewDBSink(client *gormx.Client) *DBSink { return &DBSink{client: client} }

func (s *DBSink) Migrate(ctx context.Context) error {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return fxerrors.Internal("audit: db sink requires a configured database")
	}
	return s.client.Conn(ctx).AutoMigrate(&EventRecord{})
}

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
	store *outbox.Store
	topic string
}

func NewOutboxSink(store *outbox.Store, topic string) *OutboxSink {
	return &OutboxSink{store: store, topic: topic}
}

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
