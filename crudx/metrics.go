package crudx

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/fitan/fxkit/fxerrors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 列表查询指标：耗时、返回行数、错误原因（reason 来自 fxerrors.Details）。
//
// 使用全局 MeterProvider（otelx 安装）；未启用 OTel 时是 noop。
var (
	listMetricsOnce sync.Once
	listDuration    metric.Float64Histogram
	listRows        metric.Int64Histogram
	listErrors      metric.Int64Counter
)

func initListMetrics() {
	listMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/crudx")
		listDuration, _ = m.Float64Histogram(
			"crudx.list.duration",
			metric.WithUnit("s"),
			metric.WithDescription("crudx.List duration"),
		)
		listRows, _ = m.Int64Histogram(
			"crudx.list.rows",
			metric.WithDescription("Rows returned by crudx.List"),
		)
		listErrors, _ = m.Int64Counter(
			"crudx.list.errors",
			metric.WithDescription("crudx.List failures by reason"),
		)
	})
}

func recordList(ctx context.Context, d time.Duration, rows int, err error) {
	initListMetrics()
	result := "ok"
	if err != nil {
		result = "error"
	}
	attrs := metric.WithAttributes(attribute.String("crudx.result", result))
	if listDuration != nil {
		listDuration.Record(ctx, d.Seconds(), attrs)
	}
	if listRows != nil {
		listRows.Record(ctx, int64(rows), attrs)
	}
	if err != nil && listErrors != nil {
		listErrors.Add(ctx, 1, metric.WithAttributes(
			attribute.String("crudx.error.reason", listErrorReason(err)),
		))
	}
}

// listErrorReason 提取稳定的原因码（避免把原始错误文本作为属性，造成高基数）。
func listErrorReason(err error) string {
	if err == nil {
		return "ok"
	}
	var fe *fxerrors.Error
	if errors.As(err, &fe) {
		if r, ok := fe.Details["reason"].(string); ok && r != "" {
			return r
		}
		return string(fe.Kind)
	}
	return "internal"
}
