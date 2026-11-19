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
	// ansiRed 是 ERROR 级别整行使用的 ANSI 红色前缀。
	ansiRed = "\033[31m"
	// ansiYellow 是 WARN 级别整行使用的 ANSI 黄色前缀。
	ansiYellow = "\033[33m"
	// ansiReset 重置 ANSI 颜色，写在着色行末尾。
	ansiReset = "\033[0m"
)

// defaultInstalled 记录 [SetupDefault] 是否已执行过，保证只安装一次。
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

// initDefaultHandler 是包初始化时的 slog 默认 handler。
//
// 标准库把默认 handler 实现为未导出的 *defaultHandler，没有导出的类型判断方式；
// 单靠 %T 字符串匹配会把「别的包 init 里安装的自定义 handler」误判成默认值。
// 先做指针比较（只有「从未被替换过」才相等），再用类型名做二次确认。
var initDefaultHandler = slog.Default().Handler()

// isStdlibDefault 判断 handler 是否仍为 log/slog 的默认 handler。
func isStdlibDefault(h slog.Handler) bool {
	if h == nil {
		return true
	}
	if h != initDefaultHandler {
		return false
	}
	return strings.HasSuffix(fmt.Sprintf("%T", h), "defaultHandler")
}

// step 记录一次 WithAttrs / WithGroup 调用，用于在最终 handler 上按原顺序重放。
//
// 早期实现把 attrs 与 groups 分开保存、并总是先 WithGroup 再 WithAttrs，
// 导致 `logger.With("a", 1).WithGroup("g")` 里的 a 被错误地嵌进 g（与 log/slog 语义相反）。
type step struct {
	// group 非空表示这一步是 WithGroup；为空表示这一步携带 attrs。
	group string
	// attrs 是这一步要追加的属性。
	attrs []slog.Attr
}

// sharedState 在一个 handler 及其 WithAttrs/WithGroup 派生链之间共享。
//
// 关键优化：内层 TextHandler 与缓冲区复用，避免每条日志都重建 handler
// （早期 Enabled 也会重建一次，即使该 level 最终被过滤）。
type sharedState struct {
	// mu 串行化对 buf 的写入，所有派生 handler 共用同一把锁。
	mu sync.Mutex
	// buf 是内层 TextHandler 的输出缓冲，每次 Handle 前 Reset。
	buf bytes.Buffer
}

// colorHandler 是 [NewConsoleHandler] 返回的 slog handler：内层 TextHandler 负责渲染，
// 外层按级别给整行加 ANSI 颜色。
type colorHandler struct {
	// w 是最终的日志输出目标。
	w io.Writer
	// opts 是构造时传入的 handler 选项（级别过滤与文本格式）。
	opts *slog.HandlerOptions
	// steps 记录 WithAttrs/WithGroup 的调用顺序，用于重建内层 handler。
	steps []step
	// color 表示输出目标为 TTY，需要给 WARN/ERROR 行着色。
	color bool
	// shared 是与派生 handler 共享的缓冲区与锁。
	shared *sharedState
	// inner 是按 steps 预构建的内层 TextHandler。
	inner slog.Handler

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

// Enabled 按 opts.Level 判定是否记录该级别，未设置时最低为 Info。
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

// Handle 用内层 TextHandler 渲染到共享缓冲区，再按级别给整行加 ANSI 颜色后写入 w。
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

// WithAttrs 返回追加了属性的派生 handler；attrs 落在最近一次 WithGroup 之内，空 attrs 返回自身。
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

// WithGroup 返回新增一层分组的派生 handler；空名字返回自身。
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
