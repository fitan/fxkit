package consulx

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	// ErrLockHeld 锁已被其他持有者占用
	ErrLockHeld = errors.New("consulx: lock is already held")
	// ErrLockNotHeld 尝试释放或操作未持有的锁
	ErrLockNotHeld = errors.New("consulx: lock is not held")
	// ErrLockLost 锁在持有期间由于会话失效或网络中断而丢失
	ErrLockLost = errors.New("consulx: lock was lost")
	// ErrConsulDisabled Consul 未配置或地址为空
	ErrConsulDisabled = errors.New("consulx: consul is disabled")
)

// Locker 是分布式锁工厂抽象接口。
type Locker interface {
	// NewLock 创建指定 key 的分布式锁实例。
	NewLock(key string, opts ...LockOption) (Lock, error)
	// WithLock 获取锁后执行 fn，并在执行完毕（包括 panic 时）自动释放锁。
	WithLock(ctx context.Context, key string, fn func(ctx context.Context) error, opts ...LockOption) error
}

// Lock 是单个分布式锁实例。
type Lock interface {
	// Lock 阻塞等待并获取锁，直到成功或 ctx 超时/取消。
	Lock(ctx context.Context) error
	// TryLock 尝试非阻塞获取锁，获取失败立即返回 ErrLockHeld。
	TryLock(ctx context.Context) error
	// Unlock 释放锁。
	Unlock(ctx context.Context) error
	// Key 返回锁的唯一键名。
	Key() string
	// IsHeld 返回当前进程/实例是否持有该锁。
	IsHeld() bool
}

// LockOptions 分布式锁配置选项。
type LockOptions struct {
	// TTL Session 存活时间，Consul 限制最低为 10s，默认 15s。
	TTL time.Duration
	// LockDelay 锁释放后的延迟等待时间，默认 0s。
	LockDelay time.Duration
	// RetryWait 阻塞等待获取锁时的重试或轮询间隔，默认 250ms。
	RetryWait time.Duration
	// Value 存储在锁对应 Key 上的元数据负载（如实例标识、IP等），便于监控排查。
	Value []byte
	// Prefix KV 路径前缀，默认 "locks/"。
	Prefix string
}

// LockOption 配置修改函数。
type LockOption func(*LockOptions)

// DefaultLockOptions 返回默认锁配置。
func DefaultLockOptions() LockOptions {
	return LockOptions{
		TTL:       15 * time.Second,
		LockDelay: 0,
		RetryWait: 250 * time.Millisecond,
		Prefix:    "locks/",
	}
}

// WithTTL 设置锁对应的 Session TTL（最低 10s）。
func WithTTL(ttl time.Duration) LockOption {
	return func(o *LockOptions) {
		if ttl >= 10*time.Second {
			o.TTL = ttl
		}
	}
}

// WithLockDelay 设置锁释放后的 LockDelay。
func WithLockDelay(delay time.Duration) LockOption {
	return func(o *LockOptions) {
		o.LockDelay = delay
	}
}

// WithRetryWait 设置获取锁阻塞重试间隔。
func WithRetryWait(wait time.Duration) LockOption {
	return func(o *LockOptions) {
		if wait > 0 {
			o.RetryWait = wait
		}
	}
}

// WithValue 设置锁存储的元数据信息。
func WithValue(val []byte) LockOption {
	return func(o *LockOptions) {
		o.Value = val
	}
}

// WithPrefix 设置锁键名前缀。
func WithPrefix(prefix string) LockOption {
	return func(o *LockOptions) {
		o.Prefix = prefix
	}
}

// ProvideLocker 提供 Fx 容器中的全局 Locker 实例。
// 当 discovery.consul_address 配置时，返回生产级基于 Consul Session 的分布式锁；
// 当未配置时，返回 disabledLocker（调用时显式报错 ErrConsulDisabled）。
func ProvideLocker(disc *Config) Locker {
	if disc == nil || strings.TrimSpace(disc.ConsulAddress) == "" {
		return &disabledLocker{}
	}
	l, err := NewConsulLocker(disc.ConsulAddress, nil)
	if err != nil {
		return &disabledLocker{}
	}
	return l
}

// WithLockResult 快捷在分布式锁临界区执行 fn 并返回计算结果 R。
// 利用 Go 1.27+ 泛型函数/方法，免除在外部定义临时变量。
func WithLockResult[R any](l Locker, ctx context.Context, key string, fn func(ctx context.Context) (R, error), opts ...LockOption) (R, error) {
	var zero R
	var res R
	err := l.WithLock(ctx, key, func(ctx context.Context) error {
		val, err := fn(ctx)
		if err != nil {
			return err
		}
		res = val
		return nil
	}, opts...)
	if err != nil {
		return zero, err
	}
	return res, nil
}
