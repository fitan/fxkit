package hatchetx

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hatchet-dev/hatchet/sdks/go/features"
)

var (
	everyRe    = regexp.MustCompile(`(?i)^@every\s+(\d+)\s*([smhd])$`)
	durationRe = regexp.MustCompile(`(?i)^(\d+)\s*([smhd])$`)
)

// NormalizeCronExpression converts common Dapr-style schedules into a Hatchet
// cron expression (5- or 6-field, seconds optional). Plain cron strings pass through.
//
// Supported shortcuts:
//   - @every Ns|Nm|Nh|Nd
//   - bare durations used by Actor Reminder (15s, 1m)
//   - @hourly, @daily, @weekly, @monthly
func NormalizeCronExpression(schedule string) (string, error) {
	s := strings.TrimSpace(schedule)
	if s == "" {
		return "", fmt.Errorf("cron expression is required")
	}
	switch strings.ToLower(s) {
	case "@hourly":
		return "0 * * * *", nil
	case "@daily":
		return "0 0 * * *", nil
	case "@weekly":
		return "0 0 * * 0", nil
	case "@monthly":
		return "0 0 1 * *", nil
	}
	if m := everyRe.FindStringSubmatch(s); m != nil {
		return everyToCron(m[1], m[2], schedule)
	}
	if m := durationRe.FindStringSubmatch(s); m != nil {
		// Actor Reminder period style: "15s", "1m"
		return everyToCron(m[1], m[2], schedule)
	}
	if !features.IsValidCronExpression(s) {
		return "", fmt.Errorf("invalid cron expression: %q (use standard cron, @every 15s, or 15s)", schedule)
	}
	return s, nil
}

func everyToCron(num, unit, original string) (string, error) {
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return "", fmt.Errorf("invalid duration: %q", original)
	}
	switch strings.ToLower(unit) {
	case "s":
		if n >= 60 {
			return "", fmt.Errorf("%q not supported; use minutes or a cron expression", original)
		}
		return fmt.Sprintf("*/%d * * * * *", n), nil
	case "m":
		if n >= 60 {
			return "", fmt.Errorf("%q not supported; use hours or a cron expression", original)
		}
		return fmt.Sprintf("*/%d * * * *", n), nil
	case "h":
		if n >= 24 {
			return "", fmt.Errorf("%q not supported; use a cron expression", original)
		}
		return fmt.Sprintf("0 */%d * * *", n), nil
	case "d":
		// Approximate day interval; true calendar intervals need an explicit cron expression.
		if n == 1 {
			return "0 0 * * *", nil
		}
		return "", fmt.Errorf("%q not supported for multi-day intervals; use a cron expression", original)
	default:
		return "", fmt.Errorf("invalid duration unit in %q", original)
	}
}

// ParsePeriodDuration parses reminder-style periods (15s, 1m) for validation.
func ParsePeriodDuration(period string) (time.Duration, error) {
	s := strings.TrimSpace(period)
	if s == "" {
		return 0, fmt.Errorf("period is required")
	}
	if m := durationRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid period %q", period)
		}
		switch strings.ToLower(m[2]) {
		case "s":
			return time.Duration(n) * time.Second, nil
		case "m":
			return time.Duration(n) * time.Minute, nil
		case "h":
			return time.Duration(n) * time.Hour, nil
		case "d":
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	if strings.HasPrefix(strings.ToLower(s), "@every ") {
		return ParsePeriodDuration(strings.TrimSpace(s[len("@every "):]))
	}
	return 0, fmt.Errorf("invalid period %q", period)
}
