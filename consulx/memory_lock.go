package consulx

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type memoryLocker struct {
	mu    sync.Mutex
	locks map[string]*memLockEntry
}

type memLockEntry struct {
	ch chan struct{}
}

// NewMemoryLocker 创建基于进程内内存的锁实现，常用于本地开发或单元测试。
func NewMemoryLocker() Locker {
	return &memoryLocker{
		locks: make(map[string]*memLockEntry),
	}
}

func (m *memoryLocker) getEntry(key string) *memLockEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.locks[key]
	if !ok {
		entry = &memLockEntry{
			ch: make(chan struct{}, 1),
		}
		entry.ch <- struct{}{} // 初始可用
		m.locks[key] = entry
	}
	return entry
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
	return &memoryLock{
		locker: m,
		key:    cleanKey,
		opts:   opt,
	}, nil
}

func (m *memoryLocker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error, opts ...LockOption) error {
	l, err := m.NewLock(key, opts...)
	if err != nil {
		return err
	}
	if err := l.Lock(ctx); err != nil {
		return err
	}
	defer func() {
		_ = l.Unlock(context.Background())
	}()
	return fn(ctx)
}

type memoryLock struct {
	locker *memoryLocker
	key    string
	opts   LockOptions
	mu     sync.Mutex
	isHeld bool
}

func (l *memoryLock) Key() string {
	return l.key
}

func (l *memoryLock) IsHeld() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.isHeld
}

func (l *memoryLock) TryLock(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.isHeld {
		return nil
	}

	entry := l.locker.getEntry(l.key)
	select {
	case <-entry.ch:
		l.isHeld = true
		return nil
	default:
		return ErrLockHeld
	}
}

func (l *memoryLock) Lock(ctx context.Context) error {
	l.mu.Lock()
	if l.isHeld {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()

	entry := l.locker.getEntry(l.key)
	select {
	case <-entry.ch:
		l.mu.Lock()
		l.isHeld = true
		l.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *memoryLock) Unlock(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.isHeld {
		return ErrLockNotHeld
	}

	entry := l.locker.getEntry(l.key)
	entry.ch <- struct{}{}
	l.isHeld = false
	return nil
}

// disabledLocker 当 discovery.consul_address 未配置时的空实现，调用时明确报错。
type disabledLocker struct{}

func (d *disabledLocker) NewLock(key string, opts ...LockOption) (Lock, error) {
	return nil, ErrConsulDisabled
}

func (d *disabledLocker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error, opts ...LockOption) error {
	return ErrConsulDisabled
}
