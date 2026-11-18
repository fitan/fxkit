package consulx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
)

type memoryLocker struct {
	mu    sync.Mutex
	locks map[string]*memLockEntry
	seq   atomic.Uint64 // 为 fencing token 提供进程内单调序号
}

// memLockEntry 是某个（含 Prefix 的）key 在进程内的互斥令牌。
// refs 统计当前引用它的 lock 实例（等待者 + 持有者），归零即从 map 删除，
// 避免大量一次性 key 造成内存泄漏。
type memLockEntry struct {
	token chan struct{}
	refs  int
}

// NewMemoryLocker 创建基于进程内内存的锁实现，常用于本地开发或单元测试。
// 与 Consul 实现一致，遵循 LockOptions.Prefix 命名空间语义。
func NewMemoryLocker() Locker {
	return &memoryLocker{
		locks: make(map[string]*memLockEntry),
	}
}

func (m *memoryLocker) acquireEntry(key string) *memLockEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.locks[key]
	if !ok {
		entry = &memLockEntry{
			token: make(chan struct{}, 1),
		}
		entry.token <- struct{}{} // 初始可用
		m.locks[key] = entry
	}
	entry.refs++
	return entry
}

func (m *memoryLocker) releaseEntry(key string, entry *memLockEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.refs--
	if entry.refs > 0 {
		return
	}
	if cur, ok := m.locks[key]; ok && cur == entry {
		delete(m.locks, key)
	}
}

func (m *memoryLocker) entryCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.locks)
}

func (m *memoryLocker) NewLock(key string, opts ...LockOption) (Lock, error) {
	cleanKey := strings.TrimSpace(strings.Trim(key, "/"))
	if cleanKey == "" {
		return nil, fmt.Errorf("consulx: lock key cannot be empty")
	}
	opt := DefaultLockOptions()
	for _, o := range opts {
		o(&opt)
	}
	opt = normalizeLockOptions(opt, false)
	return &memoryLock{
		locker: m,
		key:    cleanKey,
		opts:   opt,
	}, nil
}

func (m *memoryLocker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error, opts ...LockOption) (err error) {
	l, err := m.NewLock(key, opts...)
	if err != nil {
		return err
	}
	if err := l.Lock(ctx); err != nil {
		return err
	}
	defer func() {
		err = joinUnlockErr(err, key, l.Unlock(context.Background()))
	}()
	return fn(ctx)
}

type memoryLock struct {
	locker *memoryLocker
	key    string
	opts   LockOptions

	mu      sync.Mutex
	held    bool
	pending bool
	entry   *memLockEntry
	fencing atomic.Uint64
}

func (l *memoryLock) FencingToken() uint64 {
	return l.fencing.Load()
}

func (l *memoryLock) Key() string {
	return l.key
}

func (l *memoryLock) IsHeld() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held && l.entry != nil
}

func (l *memoryLock) entryKey() string {
	return l.opts.keyPath(l.key)
}

// beginAcquire 实现不可重入：已持有或另一个 goroutine 正在获取时返回 ErrLockHeld。
func (l *memoryLock) beginAcquire() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held || l.pending {
		return ErrLockHeld
	}
	l.pending = true
	return nil
}

func (l *memoryLock) endAcquire() {
	l.mu.Lock()
	l.pending = false
	l.mu.Unlock()
}

func (l *memoryLock) markHeld(entry *memLockEntry) {
	l.mu.Lock()
	l.held = true
	l.entry = entry
	l.fencing.Store(l.locker.seq.Add(1))
	l.mu.Unlock()
}

func (l *memoryLock) TryLock(ctx context.Context) error {
	if err := l.beginAcquire(); err != nil {
		return err
	}
	defer l.endAcquire()

	key := l.entryKey()
	entry := l.locker.acquireEntry(key)

	select {
	case <-entry.token:
		if err := ctx.Err(); err != nil {
			entry.token <- struct{}{}
			l.locker.releaseEntry(key, entry)
			return err
		}
		l.markHeld(entry)
		return nil
	default:
		l.locker.releaseEntry(key, entry)
		return ErrLockHeld
	}
}

func (l *memoryLock) Lock(ctx context.Context) error {
	if err := l.beginAcquire(); err != nil {
		return err
	}
	defer l.endAcquire()

	key := l.entryKey()
	entry := l.locker.acquireEntry(key)
	owned := false
	defer func() {
		if !owned {
			l.locker.releaseEntry(key, entry)
		}
	}()

	for {
		// ctx 与令牌同时就绪时优先取消：先做一次 ctx 检查。
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-entry.token:
			if err := ctx.Err(); err != nil {
				entry.token <- struct{}{}
				return err
			}
			l.markHeld(entry)
			owned = true
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (l *memoryLock) Unlock(ctx context.Context) error {
	l.mu.Lock()
	if !l.held || l.entry == nil {
		l.mu.Unlock()
		return ErrLockNotHeld
	}
	entry := l.entry
	l.held = false
	l.entry = nil
	l.fencing.Store(0)
	l.mu.Unlock()

	// 先归还令牌，再递减引用；channel 已满说明状态错乱，丢弃而不是阻塞。
	select {
	case entry.token <- struct{}{}:
	default:
		slog.Warn("consulx: memory lock token already returned", "key", l.key)
	}
	l.locker.releaseEntry(l.entryKey(), entry)
	return nil
}

// disabledLocker 在 discovery.consul_address 未配置（或配置非法）时的空实现，调用时明确报错。
type disabledLocker struct {
	err error
}

func (d *disabledLocker) Err() error {
	if d.err == nil {
		return ErrConsulDisabled
	}
	return errors.Join(ErrConsulDisabled, d.err)
}

func (d *disabledLocker) NewLock(key string, opts ...LockOption) (Lock, error) {
	return nil, d.Err()
}

func (d *disabledLocker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error, opts ...LockOption) error {
	return d.Err()
}
