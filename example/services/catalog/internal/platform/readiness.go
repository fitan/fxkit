package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/server"
)

// ReadinessCheck 是「业务级」就绪探针示例。
//
// 与 gormx / cachex 自带的连接级检查不同，它验证的是**业务可用性**：
// counter 表是否真的可读。数据库连接正常但迁移未完成时，
// 连接级 Ping 会通过而业务请求全部失败 —— 这类场景需要业务级检查。
//
// 实现 server.HealthChecker（Name + Check），由 /readyz 自动聚合并并发执行：
//
//	{"status":"ok","checks":{"catalog-counter":"ok","db":"ok"}}
type ReadinessCheck struct {
	client  *gormx.Client
	timeout time.Duration
}

// NewReadinessCheck 构造业务级检查项。
func NewReadinessCheck(client *gormx.Client) *ReadinessCheck {
	return &ReadinessCheck{client: client, timeout: 3 * time.Second}
}

// Name 返回检查项名称（出现在 /readyz 响应的 checks 键里）。
func (c *ReadinessCheck) Name() string { return "catalog-counter" }

// Check 用一次轻量查询确认 counter 表存在且可读。
// 比 Ping 更贴近真实可用性：表缺失/权限不足会在这里暴露。
func (c *ReadinessCheck) Check(ctx context.Context) error {
	if c == nil || c.client == nil || c.client.Pool() == nil {
		return fmt.Errorf("gorm client is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var n int64
	if err := c.client.Conn(ctx).Model(&Counter{}).Limit(1).Count(&n).Error; err != nil {
		return fmt.Errorf("counter table not readable: %w", err)
	}
	return nil
}

// ProvideReadiness 把业务级检查项注册到 DI group，供 /readyz 聚合。
//
// 在 platform.Module 里加一行即可：
//
//	var Module = di.Options(
//	    fxkit.Service("platform", NewService, ...),
//	    platform.ProvideReadiness(),
//	)
func ProvideReadiness() di.Option {
	return server.ProvideHealthChecker(func(client *gormx.Client) server.HealthChecker {
		if client == nil || client.Pool() == nil {
			return nil // 未配置数据库时不注册检查项
		}
		return NewReadinessCheck(client)
	})
}
