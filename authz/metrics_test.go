package authz

import (
	"context"
	"errors"
	"testing"
)

// TestResourceMetricsRecorders 覆盖 JWKS / Casbin reload 指标的记录路径。
//
// otel 未启用时全局 MeterProvider 是 noop；这里只验证成功/失败两种情况都能安全记录，
// 同时保证新增指标语句被执行（覆盖率门禁）。
func TestResourceMetricsRecorders(t *testing.T) {
	ctx := context.Background()
	recordJWKSFetch(ctx, nil)
	recordJWKSFetch(ctx, errors.New("jwks unreachable"))
	recordPolicyReload(ctx, nil)
	recordPolicyReload(ctx, errors.New("reload failed"))
}
