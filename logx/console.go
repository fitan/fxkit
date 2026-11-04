// Package logx 提供本地开发用的彩色控制台日志辅助函数。
package logx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
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
//
// 若宿主已经安装了自定义 handler（在 fxkit.Run 之前调用过 slog.SetDefault），
// 则不会覆盖它——框架不应该抢占宿主显式配置的日志出口。
func SetupDefault() {
	if !defaultInstalled.CompareAndSwap(false, true) {
		return
	}
	if !isStdlibDefault(slog.Default().Handler()) {
		return
	}
	slog.SetDefault(slog.New(NewConsoleHandler(os.Stderr, nil)))
}

// isStdlibDefault 判断 handler 是否为 log/slog 的默认 handler。
// 标准库把默认 handler 实现为未导出的 *defaultHandler。
func isStdlibDefault(h slog.Handler) bool {
	if h == nil {
		return true
	}
	return strings.HasSuffix(fmt.Sprintf("%T", h), "slog.defaultHandler")
}

// step 记录一次 WithAttrs / WithGroup 调用，用于在最终 handler 上按原顺序重放。
//
// 早期实现把 attrs 与 groups 分开保存、并总是先 WithGroup 再 WithAttrs，
// 导致 `logger.With("a", 1).WithGroup("g")` 里的 a 被错误地嵌进 g（与 log/slog 语义相反）。
type step struct {
	group string
	attrs []slog.Attr
}

// sharedState 在一个 handler 及其 WithAttrs/WithGroup 派生链之间共享。
//
// 关键优化：内层 TextHandler 与缓冲区复用，避免每条日志都重建 handler
// （早期 Enabled 也会重建一次，即使该 level 最终被过滤）。
type sharedState struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

type colorHandler struct {
	w      io.Writer
	opts   *slog.HandlerOptions
	steps  []step
	color  bool
	shared *sharedState
	inner  slog.Handler

	// initOnce 兼容直接字面量构造（测试）的场景：首次使用时惰性补齐 shared/inner。
	initOnce sync.Once
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
	h := &colorHandler{
		w:      w,
		opts:   opts,
		color:  color,
		shared: &sharedState{},
	}
	h.inner = h.buildInner()
	return h
}

func (h *colorHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts != nil && h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

// state 返回共享缓冲区，必要时惰性初始化（兼容零值/字面量构造）。
func (h *colorHandler) state() *sharedState {
	h.initOnce.Do(func() {
		if h.shared == nil {
			h.shared = &sharedState{}
		}
		if h.inner == nil {
			h.inner = h.buildInner()
		}
	})
	return h.shared
}

func (h *colorHandler) Handle(ctx context.Context, r slog.Record) error {
	s := h.state()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.buf.Reset()
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}

	color := ""
	if h.color {
		switch {
		case r.Level >= slog.LevelError:
			color = ansiRed
		case r.Level == slog.LevelWarn:
			color = ansiYellow
		}
	}
	if color != "" {
		if _, err := io.WriteString(h.w, color); err != nil {
			return err
		}
	}
	if _, err := h.w.Write(s.buf.Bytes()); err != nil {
		return err
	}
	if color != "" {
		_, err := io.WriteString(h.w, ansiReset)
		return err
	}
	return nil
}

func (h *colorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	steps := make([]step, len(h.steps), len(h.steps)+1)
	copy(steps, h.steps)
	// 与 log/slog 一致：在最近一次 WithGroup 之内的层级追加 attrs。
	if n := len(steps); n > 0 && steps[n-1].group != "" {
		last := steps[n-1]
		last.attrs = append(append([]slog.Attr{}, last.attrs...), attrs...)
		steps[n-1] = last
	} else {
		steps = append(steps, step{attrs: append([]slog.Attr{}, attrs...)})
	}
	return h.derive(steps)
}

func (h *colorHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	steps := make([]step, len(h.steps), len(h.steps)+1)
	copy(steps, h.steps)
	steps = append(steps, step{group: name})
	return h.derive(steps)
}

// derive 复制 handler（共享 buffer/lock），并按新的 steps 预构建内层 handler。
func (h *colorHandler) derive(steps []step) *colorHandler {
	out := &colorHandler{
		w:      h.w,
		opts:   h.opts,
		steps:  steps,
		color:  h.color,
		shared: h.state(),
	}
	out.inner = out.buildInner()
	return out
}

// buildInner 按调用顺序重放 WithAttrs / WithGroup，输出到共享 buffer。
func (h *colorHandler) buildInner() slog.Handler {
	var handler slog.Handler = slog.NewTextHandler(&h.shared.buf, h.opts)
	for _, s := range h.steps {
		if s.group != "" {
			handler = handler.WithGroup(s.group)
			continue
		}
		if len(s.attrs) > 0 {
			handler = handler.WithAttrs(s.attrs)
		}
	}
	return handler
}
