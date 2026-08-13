package gormx

import (
	"context"
	"log/slog"
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
	if a.level != slog.LevelInfo {
		t.Fatalf("empty default=%v", a.level)
	}
	a = newSlogAdapter("warn").(*slogAdapter)
	if a.level != slog.LevelWarn {
		t.Fatalf("warn=%v", a.level)
	}
}
