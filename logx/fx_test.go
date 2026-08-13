package logx

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx/fxevent"
)

type captureWriter struct {
	lines []string
}

func (c *captureWriter) Write(p []byte) (int, error) {
	c.lines = append(c.lines, string(p))
	return len(p), nil
}

func TestDiagnosticFxLoggerReportsActiveHookOnTimeout(t *testing.T) {
	w := &captureWriter{}
	logger := NewFxLogger(w).(*diagnosticFxLogger)

	logger.LogEvent(&fxevent.OnStartExecuting{
		FunctionName: "example/internal/demo.registerReminderOnStart.func1",
		CallerName:   "example/internal/demo.registerReminderOnStart",
	})

	logger.LogEvent(&fxevent.Started{Err: context.DeadlineExceeded})

	found := false
	for _, line := range w.lines {
		if strings.Contains(line, "timed out while running hook") &&
			strings.Contains(line, "registerReminderOnStart") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected timeout hook attribution in logs: %v", w.lines)
	}
}

func TestDiagnosticFxLoggerClearsActiveHookAfterSuccess(t *testing.T) {
	logger := NewFxLogger(&captureWriter{}).(*diagnosticFxLogger)

	logger.LogEvent(&fxevent.OnStartExecuting{FunctionName: "hook-a"})
	logger.LogEvent(&fxevent.OnStartExecuted{
		FunctionName: "hook-a",
		Runtime:      time.Millisecond,
	})

	hook, _ := logger.activeHookInfo()
	if hook != "" {
		t.Fatalf("expected no active hook, got %q", hook)
	}
}
