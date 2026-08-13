package logx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"go.uber.org/fx/fxevent"
)

type colorWriter struct {
	w     io.Writer
	color bool
}

func (cw *colorWriter) Write(p []byte) (int, error) {
	out := p
	if cw.color {
		s := string(p)
		switch {
		case strings.Contains(s, "[Fx] ERROR"):
			out = []byte(ansiRed + s + ansiReset)
		case strings.Contains(s, "failed in"):
			out = []byte(ansiRed + s + ansiReset)
		case strings.Contains(s, "timed out while running hook"):
			out = []byte(ansiRed + s + ansiReset)
		case strings.Contains(s, "[Fx] HOOK") && strings.Contains(s, "WARN"):
			out = []byte(ansiYellow + s + ansiReset)
		}
	}
	n, err := cw.w.Write(out)
	if err != nil {
		return n, err
	}
	return len(p), nil
}

type diagnosticFxLogger struct {
	inner fxevent.Logger

	mu          sync.Mutex
	activeHook  string
	activeSince time.Time
}

// NewFxLogger 返回 Fx 生命周期 logger：ERROR 行着色，启动超过 fx.StartTimeout 时明确归因超时 hook。
func NewFxLogger(w io.Writer) fxevent.Logger {
	if w == nil {
		w = os.Stderr
	}
	color := false
	if f, ok := w.(*os.File); ok {
		color = isatty.IsTerminal(f.Fd())
	}
	return &diagnosticFxLogger{
		inner: &fxevent.ConsoleLogger{W: &colorWriter{w: w, color: color}},
	}
}

func (l *diagnosticFxLogger) LogEvent(event fxevent.Event) {
	switch e := event.(type) {
	case *fxevent.OnStartExecuting:
		l.mu.Lock()
		l.activeHook = e.FunctionName
		l.activeSince = time.Now()
		l.mu.Unlock()
	case *fxevent.OnStartExecuted:
		l.mu.Lock()
		if l.activeHook == e.FunctionName {
			l.activeHook = ""
		}
		l.mu.Unlock()
		if e.Err != nil {
			slog.Error("fx startup hook failed",
				"hook", e.FunctionName,
				"caller", e.CallerName,
				"duration", e.Runtime,
				"error", e.Err,
			)
		} else if e.Runtime >= 2*time.Second {
			slog.Warn("fx startup hook slow",
				"hook", e.FunctionName,
				"caller", e.CallerName,
				"duration", e.Runtime,
			)
		}
	case *fxevent.Started:
		if e.Err != nil && isDeadlineExceeded(e.Err) {
			hook, elapsed := l.activeHookInfo()
			if hook != "" {
				slog.Error("fx startup timed out",
					"hook", hook,
					"elapsed", elapsed,
					"error", e.Err,
					"hint", "Increase fx.StartTimeout or defer heavy work out of OnStart",
				)
				l.inner.LogEvent(&fxevent.Started{
					Err: fmt.Errorf("startup timed out while running hook %s after %s: %w", hook, elapsed.Round(time.Millisecond), e.Err),
				})
				return
			}
			slog.Error("fx startup timed out", "error", e.Err)
		}
	case *fxevent.RollingBack:
		if isDeadlineExceeded(e.StartErr) {
			if hook, elapsed := l.activeHookInfo(); hook != "" {
				slog.Error("fx startup rolling back after timeout",
					"hook", hook,
					"elapsed", elapsed,
					"error", e.StartErr,
				)
			}
		}
	}

	l.inner.LogEvent(event)
}

func (l *diagnosticFxLogger) activeHookInfo() (string, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.activeHook == "" {
		return "", 0
	}
	return l.activeHook, time.Since(l.activeSince)
}

func isDeadlineExceeded(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}
