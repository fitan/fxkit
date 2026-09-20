// Package logx 提供本地开发用的彩色控制台日志辅助函数。
package logx

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/mattn/go-isatty"
)

const (
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiReset  = "\033[0m"
)

var defaultInstalled atomic.Bool

// SetupDefault 在 stderr 为 TTY 时安装彩色 slog handler。可多次调用；仅首次生效。
func SetupDefault() {
	if !defaultInstalled.CompareAndSwap(false, true) {
		return
	}
	slog.SetDefault(slog.New(NewConsoleHandler(os.Stderr, nil)))
}

type colorHandler struct {
	w      io.Writer
	opts   *slog.HandlerOptions
	attrs  []slog.Attr
	groups []string
	color  bool
	mu     *sync.Mutex
}

// NewConsoleHandler 返回在 TTY 上对 ERROR/WARN 行着色的 slog handler。
func NewConsoleHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	color := false
	if f, ok := w.(*os.File); ok {
		color = isatty.IsTerminal(f.Fd())
	}
	return &colorHandler{w: w, opts: opts, color: color, mu: &sync.Mutex{}}
}

func (h *colorHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler().Enabled(ctx, level)
}

func (h *colorHandler) Handle(ctx context.Context, r slog.Record) error {
	var buf bytes.Buffer
	if err := h.handlerTo(&buf).Handle(ctx, r); err != nil {
		return err
	}
	line := buf.Bytes()
	if h.color {
		switch {
		case r.Level >= slog.LevelError:
			line = append(append([]byte(ansiRed), line...), ansiReset...)
		case r.Level == slog.LevelWarn:
			line = append(append([]byte(ansiYellow), line...), ansiReset...)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(line)
	return err
}

func (h *colorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &next
}

func (h *colorHandler) WithGroup(name string) slog.Handler {
	next := *h
	next.groups = append(append([]string{}, h.groups...), name)
	return &next
}

func (h *colorHandler) handler() slog.Handler {
	return h.handlerTo(io.Discard)
}

func (h *colorHandler) handlerTo(w io.Writer) slog.Handler {
	var handler slog.Handler = slog.NewTextHandler(w, h.opts)
	for _, g := range h.groups {
		handler = handler.WithGroup(g)
	}
	if len(h.attrs) > 0 {
		handler = handler.WithAttrs(h.attrs)
	}
	return handler
}
