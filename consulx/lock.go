package consulx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrLockHeld 锁已被其他持有者占用；也用于表示同一 Lock 实例不可重入。
	ErrLockHeld = errors.New("consulx: lock is already held")
	// ErrLockNotHeld 尝试释放或操作未持有的锁
	ErrLockNotHeld = errors.New("consulx: lock is not held")
	// ErrLockLost 锁在持有期间由于会话失效或网络中断而丢失
	ErrLockLost = errors.New("consulx: lock was lost")
	// ErrConsulDisabled Consul 未配置或地址为空
	ErrConsulDisabled = errors.New("consulx: consul is disabled")
	// ErrInvalidLockKey 锁 key 含 Consul 不允许的字符（空格、# 等）。
	// 提前在本地报错，避免运行时拿到 Consul 的 500 "invalid key name"。
	ErrInvalidLockKey = errors.New("consulx: invalid lock key")
)

const (
	// minSessionTTL Consul Session TTL 的下限。
	minSessionTTL = 10 * time.Second
	// maxSessionTTL Consul Session TTL 的上限。
	maxSessionTTL = 24 * time.Hour
)

// Locker 是分布式锁工厂抽象接口。
type Locker interface {
	// NewLock 创建指定 key 的分布式锁实例。
	NewLock(key string, opts ...LockOption) (Lock, error)
	// WithLock 获取锁后执行 fn，并在执行完毕（包括 panic 时）自动释放锁。
	// 若 fn 返回错误且 Unlock 也失败，两者会通过 errors.Join 合并返回。
	WithLock(ctx context.Context, key string, fn func(ctx context.Context) error, opts ...LockOption) error
}

// Lock 是单个分布式锁实例。
//
// 锁实例不可重入：同一实例上的第二次 Lock/TryLock（无论是否同一 goroutine）
// 都会返回 ErrLockHeld。若需要在临界区内再次加锁，请使用另一个 key 或改用
// 独立的 Lock 实例。这样保证不同 goroutine 不可能同时进入同一实例的临界区。
type Lock interface {
	// Lock 阻塞等待并获取锁，直到成功或 ctx 超时/取消。
	// 已持有时返回 ErrLockHeld（不可重入）。
	Lock(ctx context.Context) error
	// TryLock 尝试非阻塞获取锁，获取失败或已持有时立即返回 ErrLockHeld。
	TryLock(ctx context.Context) error
	// Unlock 释放锁。重复释放返回 ErrLockNotHeld；持有期间锁已丢失时返回
	// ErrLockLost（并通过 errors.Join 附带清理阶段的错误）。
	Unlock(ctx context.Context) error
	// Key 返回锁的唯一键名。
	Key() string
	// IsHeld 返回当前进程/实例是否仍然有效持有该锁。
	// 续租协程判定会话失效后会立即返回 false。
	IsHeld() bool
}

// LockOptions 分布式锁配置选项。
type LockOptions struct {
	// TTL Session 存活时间，默认 15s。
	// Consul 允许的范围是 [10s, 24h]；低于 10s 会被提升到 10s 并记录 Warn，
	// 超过 24h 会被截断到 24h。发送给 Consul 前会向上取整到整秒，
	// 因此 10.5s 会按 11s 处理，而不是被截断成 10s。
	TTL time.Duration
	// LockDelay 锁释放后的延迟等待时间，默认 0s。
	LockDelay time.Duration
	// RetryWait 阻塞等待获取锁时的重试/阻塞查询窗口，默认 250ms。
	// 作为 Blocking Query 的 wait 参数使用（下限 100ms），同时作为失败退避基数。
	RetryWait time.Duration
	// Value 存储在锁对应 Key 上的元数据负载（如实例标识、IP等），便于监控排查。
	Value []byte
	// Prefix KV 路径前缀，默认 "locks/"。内存实现也遵循该前缀语义。
	Prefix string
	// OnLost 在持有期间续约失败、锁已失效时回调（在独立 goroutine 中执行）。
	// 业务应在此尽快停止临界区工作 —— 锁丢失意味着其他实例可能已进入。
	// 回调中的 panic 会被 recover，不会终止进程。
	OnLost func(key string, err error)
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

// WithTTL 设置锁对应的 Session TTL。
//
// 小于 10s 的值会被提升到 10s（Consul 下限）并记录 Warn；超过 24h 的值会被
// 截断到 24h。发送给 Consul 时会向上取整到整秒（如 1.5s → 2s，但仍受 10s 下限约束）。
func WithTTL(ttl time.Duration) LockOption {
	return func(o *LockOptions) {
		o.TTL = ttl
	}
}

// WithLockDelay 设置锁释放后的 LockDelay。
func WithLockDelay(delay time.Duration) LockOption {
	return func(o *LockOptions) {
		o.LockDelay = delay
	}
}

// WithRetryWait 设置获取锁阻塞重试间隔，同时作为 Blocking Query 的等待窗口。
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

// WithOnLost 注册锁丢失回调（续约连续失败、Session 已失效时触发）。
func WithOnLost(fn func(key string, err error)) LockOption {
	return func(o *LockOptions) {
		o.OnLost = fn
	}
}

// invalidKeyChar 匹配 Consul KV key 不允许的字符。
//
// Consul 只接受 `[a-zA-Z0-9,_./\-?&=]`；空格、`#`、`%` 等会在服务端报
// "invalid key name"（HTTP 500）。本地校验能把这种错配变成清晰的调用方错误。
var invalidKeyChar = regexp.MustCompile(`[^a-zA-Z0-9,_./\-?&=]`)

// validateLockKey 校验 key 是否符合 Consul 的字符集要求。
func validateLockKey(key string) error {
	if bad := invalidKeyChar.FindString(key); bad != "" {
		return fmt.Errorf("%w: %q contains %q (allowed: letters, digits, and , _ . / - ? & =)", ErrInvalidLockKey, key, bad)
	}
	return nil
}

// normalizeLockOptions 修正越界配置。warn=true 时对 TTL 修正记录告警。
func normalizeLockOptions(o LockOptions, warn bool) LockOptions {
	if o.TTL <= 0 {
		o.TTL = 15 * time.Second
	}
	if o.TTL < minSessionTTL {
		if warn {
			slog.Warn("consulx: lock TTL below Consul minimum, clamped",
				"ttl", o.TTL, "min", minSessionTTL)
		}
		o.TTL = minSessionTTL
	}
	if o.TTL > maxSessionTTL {
		if warn {
			slog.Warn("consulx: lock TTL above Consul maximum, clamped",
				"ttl", o.TTL, "max", maxSessionTTL)
		}
		o.TTL = maxSessionTTL
	}
	if o.RetryWait <= 0 {
		o.RetryWait = 250 * time.Millisecond
	}
	if o.LockDelay < 0 {
		o.LockDelay = 0
	}
	return o
}

// ttlSecondsCeil 向上取整到秒，并夹在 Consul 允许范围内。
func ttlSecondsCeil(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	if min := int(minSessionTTL / time.Second); s < min {
		s = min
	}
	if max := int(maxSessionTTL / time.Second); s > max {
		s = max
	}
	return s
}

// lockDelaySeconds 将 LockDelay 向上取整到秒（不做 TTL 下限约束）。
func lockDelaySeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(math.Ceil(d.Seconds()))
}

// keyPath 将 Prefix 与业务 key 组合成最终路径，用于 Consul KV 与内存实现的命名空间。
func (o LockOptions) keyPath(key string) string {
	prefix := strings.Trim(o.Prefix, "/")
	if prefix == "" {
		return key
	}
	return prefix + "/" + key
}

// BuildLocker 按 discovery 配置构建 Locker。
// consul_address 为空时返回 disabledLocker（调用时返回 ErrConsulDisabled）；
// 地址非法时返回错误，避免静默降级为不可用实现。
func BuildLocker(disc *Config) (Locker, error) {
	if disc == nil || strings.TrimSpace(disc.ConsulAddress) == "" {
		return &disabledLocker{}, nil
	}
	return NewConsulLocker(disc.ConsulAddress, nil)
}

// ProvideLocker 提供 Fx 容器中的全局 Locker 实例（兼容旧 API）。
//
// 当 discovery.consul_address 为空时返回 disabledLocker；地址非法时同样返回
// disabledLocker，但其调用会返回包含配置错误的 ErrConsulDisabled（并记录 Warn）。
// 需要把配置错误暴露为启动失败的场景请使用 BuildLocker。
func ProvideLocker(disc *Config) Locker {
	l, err := BuildLocker(disc)
	if err != nil {
		slog.Warn("consulx: invalid consul address, locker disabled", "error", err)
		return &disabledLocker{err: err}
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

// joinUnlockErr 把 Unlock 错误附加到已有错误上，便于 WithLock 报告释放失败。
func joinUnlockErr(err error, key string, unlockErr error) error {
	if unlockErr == nil {
		return err
	}
	return errors.Join(err, fmt.Errorf("consulx: unlock %q: %w", key, unlockErr))
}
