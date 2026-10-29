package hatchetx

import (
	"context"
	"fmt"
	"strings"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	"github.com/hatchet-dev/hatchet/pkg/client/types"
	"github.com/hatchet-dev/hatchet/sdks/go/features"
)

// RateLimitDuration aliases Hatchet's native rate limit time windows.
type RateLimitDuration = types.RateLimitDuration

const (
	RateLimitSecond RateLimitDuration = types.Second
	RateLimitMinute RateLimitDuration = types.Minute
	RateLimitHour   RateLimitDuration = types.Hour
	RateLimitDay    RateLimitDuration = types.Day
	RateLimitWeek   RateLimitDuration = types.Week
	RateLimitMonth  RateLimitDuration = types.Month
	RateLimitYear   RateLimitDuration = types.Year
)

// UpsertRateLimit creates or updates a global rate limit rule.
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

// ListRateLimits retrieves all configured rate limits matching the criteria.
func (c *Client) ListRateLimits(ctx context.Context, opts *rest.RateLimitListParams) (*rest.RateLimitList, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	rlClient := c.SDK.RateLimits()
	if rlClient == nil {
		return nil, fmt.Errorf("hatchetx: ratelimits client not available")
	}
	return rlClient.List(ctx, opts)
}
