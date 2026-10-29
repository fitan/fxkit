package hatchetx

import (
	"context"
	"strings"
	"testing"
)

func TestRateLimits_DisabledClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := &Client{}

	if err := c.UpsertRateLimit(ctx, "api-key", 100, RateLimitMinute); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	if _, err := c.ListRateLimits(ctx, nil); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}
}

func TestRateLimits_Constants(t *testing.T) {
	t.Parallel()
	if RateLimitSecond != "second" || RateLimitMinute != "minute" || RateLimitHour != "hour" {
		t.Fatalf("unexpected rate limit durations")
	}
}
