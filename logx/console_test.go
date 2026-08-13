package logx

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestColorHandlerColorsErrors(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(&colorHandler{w: &buf, opts: &slog.HandlerOptions{}, color: true})
	logger.Error("boom")
	out := buf.String()
	if !strings.Contains(out, ansiRed) {
		t.Fatalf("expected red ANSI in %q", out)
	}
	if !strings.Contains(out, "boom") {
		t.Fatalf("expected message in %q", out)
	}
}

func TestColorHandlerColorsWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(&colorHandler{w: &buf, opts: &slog.HandlerOptions{}, color: true})
	logger.Warn("slow")
	out := buf.String()
	if !strings.Contains(out, ansiYellow) {
		t.Fatalf("expected yellow ANSI in %q", out)
	}
}

func TestFxLoggerRedactsNothing(t *testing.T) {
	logger := NewFxLogger(nopWriter{})
	if logger == nil {
		t.Fatal("nil logger")
	}
}
