package hatchetx

import "testing"

func TestNormalizeCronExpression(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"@every 15s", "*/15 * * * * *"},
		{"@every 30s", "*/30 * * * * *"},
		{"@every 1m", "*/1 * * * *"},
		{"@every 5m", "*/5 * * * *"},
		{"15s", "*/15 * * * * *"},
		{"1m", "*/1 * * * *"},
		{"@hourly", "0 * * * *"},
		{"@daily", "0 0 * * *"},
		{"1d", "0 0 * * *"},
		{"0 2 * * *", "0 2 * * *"},
		{"*/15 * * * * *", "*/15 * * * * *"},
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

func TestNormalizeCronExpression_Invalid(t *testing.T) {
	for _, in := range []string{"", "not-a-cron", "@every 0s", "@every 90s", "@every 2d", "2d"} {
		if _, err := NormalizeCronExpression(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}
