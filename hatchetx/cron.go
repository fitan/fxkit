package hatchetx

import (
	"fmt"
	"strings"
	"time"

	"github.com/hatchet-dev/hatchet/sdks/go/features"
)

// NormalizeCronExpression validates standard Hatchet cron expressions (5- or 6-field, seconds optional)
// and expands standard cron aliases (@hourly, @daily, @weekly, @monthly, @yearly, @annually).
//
// Note: @every duration syntax is intentionally not supported to prevent cron modulo boundary
// issues (e.g. */45 uneven intervals). Please use explicit standard cron expressions instead
// (e.g. "*/15 * * * * *" for every 15 seconds, or "0 * * * *" for hourly).
func NormalizeCronExpression(schedule string) (string, error) {
	s := strings.TrimSpace(schedule)
	if s == "" {
		return "", fmt.Errorf("cron expression is required")
	}
	if strings.HasPrefix(strings.ToLower(s), "@every") {
		return "", fmt.Errorf("@every is not supported to avoid cron boundary interval issues; use standard cron expressions (e.g. '*/15 * * * * *' or '0 * * * *')")
	}
	switch strings.ToLower(s) {
	case "@hourly":
		return "0 * * * *", nil
	case "@daily", "@midnight":
		return "0 0 * * *", nil
	case "@weekly":
		return "0 0 * * 0", nil
	case "@monthly":
		return "0 0 1 * *", nil
	case "@yearly", "@annually":
		return "0 0 1 1 *", nil
	}
	if !features.IsValidCronExpression(s) {
		return "", fmt.Errorf("invalid cron expression: %q (use standard cron like '*/15 * * * * *', '0 * * * *', or '@hourly')", schedule)
	}
	return s, nil
}

// ParsePeriodDuration parses standard duration strings or cron aliases into time.Duration.
func ParsePeriodDuration(period string) (time.Duration, error) {
	s := strings.TrimSpace(period)
	if s == "" {
		return 0, fmt.Errorf("period is required")
	}
	if strings.HasPrefix(strings.ToLower(s), "@every") {
		return 0, fmt.Errorf("@every is not supported; use standard duration or cron expression")
	}
	switch strings.ToLower(s) {
	case "@hourly":
		return time.Hour, nil
	case "@daily", "@midnight":
		return 24 * time.Hour, nil
	case "@weekly":
		return 7 * 24 * time.Hour, nil
	case "@monthly":
		return 30 * 24 * time.Hour, nil
	case "@yearly", "@annually":
		return 365 * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("invalid period %q: duration must be positive", period)
		}
		return d, nil
	}
	return 0, fmt.Errorf("invalid period %q", period)
}
