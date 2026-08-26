package gormx

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm/logger"
)

func TestSlogAdapterRespectsSilent(t *testing.T) {
	a := newSlogAdapter("silent").(*slogAdapter)
	if a.level <= slog.LevelError {
		t.Fatalf("silent level should be above Error, got %v", a.level)
	}
	// Trace must not panic under silent.
	a.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 1
	}, nil)
}

func TestSlogAdapterLogMode(t *testing.T) {
	a := newSlogAdapter("info").(*slogAdapter)
	next := a.LogMode(logger.Silent).(*slogAdapter)
	if next.level <= slog.LevelError {
		t.Fatalf("LogMode(Silent) level=%v", next.level)
	}
	if a.level != slog.LevelInfo {
		t.Fatalf("original mutated: %v", a.level)
	}
}

func TestParseLogLevelDefaults(t *testing.T) {
	a := newSlogAdapter("").(*slogAdapter)
	if a.level != slog.LevelWarn {
		t.Fatalf("empty default=%v", a.level)
	}
	a = newSlogAdapter("warn").(*slogAdapter)
	if a.level != slog.LevelWarn {
		t.Fatalf("warn=%v", a.level)
	}
	a = newSlogAdapter("info").(*slogAdapter)
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
