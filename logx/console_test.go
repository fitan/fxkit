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

// BenchmarkConsoleHandlerHandle 验证每条日志不再重建内层 handler。
func BenchmarkConsoleHandlerHandle(b *testing.B) {
	h := NewConsoleHandler(nopWriter{}, nil).
		WithAttrs([]slog.Attr{slog.String("service", "api")}).
		WithGroup("req")
	logger := slog.New(h)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info("request done", "status", 200, "path", "/articles")
	}
}
