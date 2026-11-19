package hatchetx

import (
	"context"
	"fmt"
	"strings"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	"github.com/hatchet-dev/hatchet/pkg/client/types"
	"github.com/hatchet-dev/hatchet/sdks/go/features"
)

// RateLimitDuration 是 Hatchet 原生限流时间窗口的别名。
type RateLimitDuration = types.RateLimitDuration

// RateLimitListParams 是列出限流规则的查询选项别名。
type RateLimitListParams = rest.RateLimitListParams

// RateLimitList 是列出限流规则结果的别名。
type RateLimitList = rest.RateLimitList

const (
	// RateLimitSecond 是 1 秒的限流窗口。
	RateLimitSecond RateLimitDuration = types.Second
	// RateLimitMinute 是 1 分钟的限流窗口。
	RateLimitMinute RateLimitDuration = types.Minute
	// RateLimitHour 是 1 小时的限流窗口。
	RateLimitHour RateLimitDuration = types.Hour
	// RateLimitDay 是 1 天的限流窗口。
	RateLimitDay RateLimitDuration = types.Day
	// RateLimitWeek 是 1 周的限流窗口。
	RateLimitWeek RateLimitDuration = types.Week
	// RateLimitMonth 是 1 个月的限流窗口。
	RateLimitMonth RateLimitDuration = types.Month
	// RateLimitYear 是 1 年的限流窗口。
	RateLimitYear RateLimitDuration = types.Year
)

// UpsertRateLimit 创建或更新全局限流规则。
func (c *Client) UpsertRateLimit(ctx context.Context, key string, limit int, duration RateLimitDuration) error {
	if !c.Enabled() {
		return fmt.Errorf("hatchetx: client disabled")
	}
	rlClient := c.SDK.RateLimits()
	if rlClient == nil {
		return fmt.Errorf("hatchetx: ratelimits client not available")
	}
	k := strings.TrimSpace(key)
	if k == "" {
		return fmt.Errorf("hatchetx: rate limit key is required")
	}
	if limit <= 0 {
		return fmt.Errorf("hatchetx: limit must be positive, got %d", limit)
	}
	return rlClient.Upsert(features.CreateRatelimitOpts{
		Key:      k,
		Limit:    limit,
		Duration: duration,
	})
}

// ListRateLimits 获取所有符合条件已配置的限流规则。
func (c *Client) ListRateLimits(ctx context.Context, opts *RateLimitListParams) (*RateLimitList, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	rlClient := c.SDK.RateLimits()
	if rlClient == nil {
		return nil, fmt.Errorf("hatchetx: ratelimits client not available")
	}
	return rlClient.List(ctx, opts)
}
