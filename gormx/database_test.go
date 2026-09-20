package gormx

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSlogAdapterRespectsSilent(t *testing.T) {
	a := newSlogAdapter("silent", 0).(*slogAdapter)
	if a.level <= slog.LevelError {
		t.Fatalf("silent level should be above Error, got %v", a.level)
	}
	// Trace must not panic under silent.
	a.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 1
	}, nil)
}

func TestSlogAdapterLogMode(t *testing.T) {
	a := newSlogAdapter("info", 0).(*slogAdapter)
	next := a.LogMode(logger.Silent).(*slogAdapter)
	if next.level <= slog.LevelError {
		t.Fatalf("LogMode(Silent) level=%v", next.level)
	}
	if a.level != slog.LevelInfo {
		t.Fatalf("original mutated: %v", a.level)
	}
}

func TestParseLogLevelDefaults(t *testing.T) {
	a := newSlogAdapter("", 0).(*slogAdapter)
	if a.level != slog.LevelWarn {
		t.Fatalf("empty default=%v", a.level)
	}
	a = newSlogAdapter("warn", 0).(*slogAdapter)
	if a.level != slog.LevelWarn {
		t.Fatalf("warn=%v", a.level)
	}
	a = newSlogAdapter("info", 0).(*slogAdapter)
	if a.level != slog.LevelInfo {
		t.Fatalf("info=%v", a.level)
	}
}

func TestPrepareDSN_sqlitePragmas(t *testing.T) {
	got := prepareDSN("sqlite", "file:./app.db?cache=shared")
	if !strings.Contains(got, "_busy_timeout=5000") {
		t.Fatalf("missing busy_timeout: %s", got)
	}
	if !strings.Contains(got, "_journal_mode=WAL") {
		t.Fatalf("missing journal_mode: %s", got)
	}
	if !strings.Contains(got, "cache=shared") {
		t.Fatalf("dropped cache: %s", got)
	}
	custom := prepareDSN("sqlite", "file:app.db?_busy_timeout=1000")
	if !strings.Contains(custom, "_busy_timeout=1000") || strings.Contains(custom, "_busy_timeout=5000") {
		t.Fatalf("should keep user busy_timeout: %s", custom)
	}
	pg := prepareDSN("postgres", "postgres://x/y")
	if pg != "postgres://x/y" {
		t.Fatalf("postgres dsn mutated: %s", pg)
	}
}

func TestSlogAdapterIgnoresRecordNotFound(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	a := newSlogAdapter("info", 0).(*slogAdapter)
	a.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT * FROM t WHERE id=1", 0
	}, gorm.ErrRecordNotFound)
	if strings.Contains(buf.String(), "database error") {
		t.Fatalf("record not found logged as error: %s", buf.String())
	}

	buf.Reset()
	a.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 0
	}, errors.New("connection reset"))
	if !strings.Contains(buf.String(), "database error") {
		t.Fatalf("real error not logged: %s", buf.String())
	}
}

func TestSlogAdapterLogsSlowQuery(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Level is Warn, threshold is 50ms
	a := newSlogAdapter("warn", 50*time.Millisecond).(*slogAdapter)

	// Simulate fast query (1ms ago)
	a.Trace(context.Background(), time.Now().Add(-1*time.Millisecond), func() (string, int64) {
		return "SELECT fast", 1
	}, nil)
	if strings.Contains(buf.String(), "database slow query") {
		t.Fatalf("fast query logged as slow: %s", buf.String())
	}

	// Simulate slow query (100ms ago)
	a.Trace(context.Background(), time.Now().Add(-100*time.Millisecond), func() (string, int64) {
		return "SELECT slow", 1
	}, nil)
	if !strings.Contains(buf.String(), "database slow query") {
		t.Fatalf("slow query not logged: %s", buf.String())
	}
}
