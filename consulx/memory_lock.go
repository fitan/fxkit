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

// memoryLocker 是基于进程内 map 的 Locker 实现，仅供本地开发与测试使用。
type memoryLocker struct {
	// mu 保护 locks。
	mu sync.Mutex
	// locks 按 Prefix+key 记录互斥令牌，引用归零即删除。
	locks map[string]*memLockEntry
	// seq 由同一 locker 的所有锁实例共享，只增不减（Unlock 不回退）。
	seq atomic.Uint64 // 为 fencing token 提供进程内单调序号
}

// memLockEntry 是某个（含 Prefix 的）key 在进程内的互斥令牌。
// refs 统计当前引用它的 lock 实例（等待者 + 持有者），归零即从 map 删除，
// 避免大量一次性 key 造成内存泄漏。
type memLockEntry struct {
	// token 容量为 1 的通道：有值表示空闲，被取走表示已被持有。
	token chan struct{}
	// refs 当前引用该条目的 lock 实例数（等待者 + 持有者）。
	refs int
}

// NewMemoryLocker 创建基于进程内内存的锁实现，常用于本地开发或单元测试。
// 与 Consul 实现一致，遵循 LockOptions.Prefix 命名空间语义。
func NewMemoryLocker() Locker {
	return &memoryLocker{
		locks: make(map[string]*memLockEntry),
	}
}

// acquireEntry 返回该 key 的互斥令牌（不存在则创建），并把引用计数加一。
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

// releaseEntry 递减引用计数，归零时从 map 删除该 key 的条目。
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

// entryCount 返回当前存活的 key 条目数（仅测试使用）。
func (m *memoryLocker) entryCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.locks)
}

// NewLock 去除首尾空白与 "/" 后校验 key 非空，套用默认选项并归一化后创建内存锁实例。
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

// WithLock 获取锁后执行 fn，结束时用 context.Background() 释放锁；
// fn 与 Unlock 的错误通过 errors.Join 合并返回。
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

// memoryLock 是进程内的 Lock 实现，同一实例不可重入。
type memoryLock struct {
	// locker 创建该锁的 Locker，提供全局 fencing 序号。
	locker *memoryLocker
	// key 业务 key（不含 Prefix）。
	key string
	// opts 归一化后的锁选项。
	opts LockOptions

	// mu 保护 held/pending/entry。
	mu sync.Mutex
	// held 当前是否持有令牌。
	held bool
	// pending 获取中，用于拒绝并发与重入。
	pending bool
	// entry 当前持有的令牌条目；未持有时为 nil。
	entry *memLockEntry
	// fencing 最近一次加锁分配的进程内序号（0 = 未持有）。
	fencing atomic.Uint64
}

// FencingToken 返回最近一次加锁分配的进程内单调序号；未持有时为 0。
func (l *memoryLock) FencingToken() uint64 {
	return l.fencing.Load()
}

// Key 返回锁的业务 key（不含 Prefix）。
func (l *memoryLock) Key() string {
	return l.key
}

// IsHeld 返回本实例当前是否持有令牌。
func (l *memoryLock) IsHeld() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held && l.entry != nil
}

// entryKey 返回 Prefix 与 key 拼成的命名空间键。
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

// endAcquire 清除 pending 标记，结束一次获取尝试。
func (l *memoryLock) endAcquire() {
	l.mu.Lock()
	l.pending = false
	l.mu.Unlock()
}

// markHeld 登记持有状态，并从 locker 的全局序号分配 fencing token。
func (l *memoryLock) markHeld(entry *memLockEntry) {
	l.mu.Lock()
	l.held = true
	l.entry = entry
	l.fencing.Store(l.locker.seq.Add(1))
	l.mu.Unlock()
}

// TryLock 非阻塞获取：令牌不可用时立即返回 ErrLockHeld；
// ctx 已取消时归还令牌并返回 ctx 错误。
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

// Lock 阻塞等待令牌直到成功或 ctx 结束；ctx 与令牌同时就绪时优先返回 ctx 错误。
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

// Unlock 归还令牌并清除持有状态；未持有时返回 ErrLockNotHeld。
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
	// err 附加原因；非 nil 时与 ErrConsulDisabled 一起通过 errors.Join 返回。
	err error
}

// Err 返回 ErrConsulDisabled；构造时带有原因则用 errors.Join 一并返回。
func (d *disabledLocker) Err() error {
	if d.err == nil {
		return ErrConsulDisabled
	}
	return errors.Join(ErrConsulDisabled, d.err)
}

// NewLock 不创建锁，始终返回 Err()。
func (d *disabledLocker) NewLock(key string, opts ...LockOption) (Lock, error) {
	return nil, d.Err()
}

// WithLock 不执行 fn，直接返回 Err()。
func (d *disabledLocker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error, opts ...LockOption) error {
	return d.Err()
}
