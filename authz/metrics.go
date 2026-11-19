package authz

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 业务指标：Casbin 判定耗时与结果（allow / deny / error）。
var (
	// authzMetricsOnce 保证指标只注册一次。
	authzMetricsOnce sync.Once
	// enforceDuration 记录 Casbin 判定耗时，单位秒。
	enforceDuration metric.Float64Histogram
	// enforceResults 统计判定结果，authz.result 取值 allow/deny/error。
	enforceResults metric.Int64Counter
	// jwksResults 统计 JWKS 拉取结果，authz.result 取值 ok/error。
	jwksResults metric.Int64Counter
	// reloadResults 统计策略重载结果，authz.result 取值 ok/error。
	reloadResults metric.Int64Counter
)

// initAuthzMetrics 通过 sync.Once 惰性创建 Casbin 判定指标。
func initAuthzMetrics() {
	authzMetricsOnce.Do(func() {
		m := otel.Meter("github.com/fitan/fxkit/authz")
		enforceDuration, _ = m.Float64Histogram(
			"authz.enforce.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Casbin enforcement duration"),
		)
		enforceResults, _ = m.Int64Counter(
			"authz.enforce.results",
			metric.WithDescription("Casbin enforcement outcomes"),
		)
		jwksResults, _ = m.Int64Counter(
			"authz.jwks.fetch",
			metric.WithDescription("JWKS fetch outcomes during token validation"),
		)
		reloadResults, _ = m.Int64Counter(
			"authz.casbin.reload",
			metric.WithDescription("Casbin policy reload outcomes"),
		)
	})
}

// recordEnforce 记录一次判定的耗时与结果（allow/deny/error）。
func recordEnforce(ctx context.Context, d time.Duration, allowed bool, err error) {
	initAuthzMetrics()
	result := "deny"
	switch {
	case err != nil:
		result = "error"
	case allowed:
		result = "allow"
	}
	if enforceDuration != nil {
		enforceDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String("authz.result", result)))
	}
	if enforceResults != nil {
		enforceResults.Add(ctx, 1, metric.WithAttributes(attribute.String("authz.result", result)))
	}
}

// recordJWKSFetch 记录一次 JWKS 拉取结果（ok / error），用于发现公钥轮换或网络故障。
func recordJWKSFetch(ctx context.Context, err error) {
	initAuthzMetrics()
	if jwksResults == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	jwksResults.Add(ctx, 1, metric.WithAttributes(attribute.String("authz.result", result)))
}

// recordPolicyReload 记录一次 Casbin 策略重载结果（ok / error）。
func recordPolicyReload(ctx context.Context, err error) {
	initAuthzMetrics()
	if reloadResults == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	reloadResults.Add(ctx, 1, metric.WithAttributes(attribute.String("authz.result", result)))
}
