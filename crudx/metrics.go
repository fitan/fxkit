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
	// listMetricsOnce 保证列表指标只注册一次。
	listMetricsOnce sync.Once
	// listDuration 记录 crudx.list.duration（单次 List 耗时，单位秒）。
	listDuration metric.Float64Histogram
	// listRows 记录 crudx.list.rows（单次 List 返回的行数）。
	listRows metric.Int64Histogram
	// listErrors 统计 crudx.list.errors（按错误原因码）。
	listErrors metric.Int64Counter
)

// initListMetrics 用全局 MeterProvider 惰性创建列表查询指标，仅执行一次。
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

// recordList 记录一次 List 的耗时、返回行数与错误原因。
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
