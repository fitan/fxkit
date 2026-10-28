package hatchetx

import (
	"testing"
	"time"
)

func TestNormalizeCronExpression(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0 * * * *", "0 * * * *"},
		{"0 0 * * *", "0 0 * * *"},
		{"0 2 * * *", "0 2 * * *"},
		{"*/15 * * * * *", "*/15 * * * * *"},
		{"*/30 * * * * *", "*/30 * * * * *"},
		{"*/5 * * * *", "*/5 * * * *"},
		{"@hourly", "0 * * * *"},
		{"@daily", "0 0 * * *"},
		{"@midnight", "0 0 * * *"},
		{"@weekly", "0 0 * * 0"},
		{"@monthly", "0 0 1 * *"},
		{"@yearly", "0 0 1 1 *"},
		{"@annually", "0 0 1 1 *"},
	}
	for _, tc := range cases {
		got, err := NormalizeCronExpression(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeCronExpression_RejectsEveryAndInvalid(t *testing.T) {
	for _, in := range []string{
		"",
		"not-a-cron",
		"@every 15s",
		"@every 1m",
		"@every 0s",
		"@every 45m",
		"15s",
		"1m",
		"2d",
	} {
		if _, err := NormalizeCronExpression(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}

func TestParsePeriodDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"15s", 15 * time.Second},
		{"1m", time.Minute},
		{"5m", 5 * time.Minute},
		{"1h", time.Hour},
		{"24h", 24 * time.Hour},
		{"@hourly", time.Hour},
		{"@daily", 24 * time.Hour},
		{"@weekly", 7 * 24 * time.Hour},
		{"@monthly", 30 * 24 * time.Hour},
	}
	for _, tc := range cases {
		got, err := ParsePeriodDuration(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %v want %v", tc.in, got, tc.want)
		}
	}

	for _, invalid := range []string{"", "invalid", "0s", "-1m", "@every 15s"} {
		if _, err := ParsePeriodDuration(invalid); err == nil {
			t.Fatalf("expected error for invalid period %q", invalid)
		}
	}
}
