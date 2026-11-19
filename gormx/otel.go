package gormx

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// 事务边界 span：otelsql 只能看到单条 SQL，这里补上「一次事务包含多少条语句、耗时多久」。
//
// 属性只放 db.system（驱动名，基数有限）；不放 DSN/表名，避免敏感信息与高基数。
var txTracer = otel.Tracer("github.com/fitan/fxkit/gormx")

// startTxSpan 开启事务边界 span；driver 非空时写入 db.system 属性。
func startTxSpan(ctx context.Context, driver string) (context.Context, trace.Span) {
	var attrs []attribute.KeyValue
	if driver != "" {
		attrs = append(attrs, attribute.String("db.system", driver))
	}
	return txTracer.Start(ctx, "db.transaction", trace.WithAttributes(attrs...))
}

// endTxSpan 结束事务 span：有错误则 RecordError 并标记 rollback，否则标记 commit。
func endTxSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "rollback")
	} else {
		span.SetAttributes(attribute.String("db.transaction.result", "commit"))
	}
	span.End()
}
