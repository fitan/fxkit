package cachex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"golang.org/x/sync/singleflight"
)

// nullPlaceholder 用于标记空值（如数据在 DB 中不存在），防止缓存穿透。
var nullPlaceholder = []byte("<<__FXKIT_CACHE_NULL__>>")

var sfGroup singleflight.Group

// Options 针对单次 GetOrSet 操作的高级配置。
type Options struct {
	// TTL 单次缓存过期时间。若 <= 0 则使用全局配置中的 DefaultTTL。
	TTL time.Duration
	// DisableSingleflight 临时禁用该 key 的 singleflight 并发合并。
	DisableSingleflight bool
	// CacheNull 是否缓存空值（当 loader 返回特定 ErrNotFound 时）。默认 true。
	CacheNull bool
	// NullTTL 空值缓存时间。若 <= 0 则使用全局配置中的 NullTTL。
	NullTTL time.Duration
}

// Option 定义 Options 修改器函数。
type Option func(*Options)

// WithTTL 指定单次缓存过期时间。
func WithTTL(ttl time.Duration) Option {
	return func(o *Options) {
		o.TTL = ttl
	}
}

// WithoutSingleflight 显式禁用 singleflight。
func WithoutSingleflight() Option {
	return func(o *Options) {
		o.DisableSingleflight = true
	}
}

// WithoutCacheNull 禁用空值缓存。
func WithoutCacheNull() Option {
	return func(o *Options) {
		o.CacheNull = false
	}
}

// WithNullTTL 指定空值缓存时间。
func WithNullTTL(ttl time.Duration) Option {
	return func(o *Options) {
		o.NullTTL = ttl
	}
}

// IsNullVal 检查缓存的二进制是否为空值占位符。
func IsNullVal(data []byte) bool {
	return string(data) == string(nullPlaceholder)
}

// JitterTTL 为传入的 ttl 附加 ±maxPercent% 的随机浮动，用于防止大批量 key 同时过期造成雪崩。
func JitterTTL(ttl time.Duration, maxPercent int) time.Duration {
	if ttl <= 0 || maxPercent <= 0 {
		return ttl
	}
	if maxPercent > 50 {
		maxPercent = 50
	}
	// 计算范围 [-maxPercent, +maxPercent]
	deltaPercent := rand.Intn(maxPercent*2+1) - maxPercent
	delta := time.Duration(int64(ttl) * int64(deltaPercent) / 100)
	jittered := ttl + delta
	if jittered <= 0 {
		return ttl
	}
	return jittered
}

// GetOrSet 是通用的泛型 Cache-Aside 模式辅助函数。
// 优先从 cache 获取并反序列化为 T；若缓存未命中，则执行 loader 从数据源加载，序列化后写入 cache。
// 内置 Singleflight（防击穿）、空值缓存（防穿透）及 TTL 抖动（防雪崩）。
func GetOrSet[T any](
	ctx context.Context,
	c Cache,
	key string,
	loader func(ctx context.Context) (T, error),
	opts ...Option,
) (T, error) {
	var zero T
	if c == nil {
		if loader == nil {
			return zero, errors.New("cache: loader cannot be nil")
		}
		return loader(ctx)
	}

	opt := Options{
		TTL:       30 * time.Minute,
		CacheNull: true,
		NullTTL:   1 * time.Minute,
	}
	for _, fn := range opts {
		fn(&opt)
	}

	// 1. 尝试从缓存读取
	valBytes, err := c.Get(ctx, key)
	if err == nil {
		if IsNullVal(valBytes) {
			return zero, ErrCacheMiss
		}
		var target T
		if unmarshalErr := json.Unmarshal(valBytes, &target); unmarshalErr == nil {
			return target, nil
		}
	}

	// 2. 如果缓存未配置/已禁用，直接透传 loader
	if errors.Is(err, ErrCacheDisabled) {
		if loader == nil {
			return zero, errors.New("cache: loader cannot be nil")
		}
		return loader(ctx)
	}

	// 3. 执行 loader 加载（带 Singleflight 防击穿）
	executeLoader := func() (T, error) {
		if loader == nil {
			return zero, errors.New("cache: loader cannot be nil")
		}
		data, loadErr := loader(ctx)
		if loadErr != nil {
			// 如果启用空值缓存且属于“未找到”，写入空值占位符防穿透
			if opt.CacheNull && isNotFoundError(loadErr) && opt.NullTTL > 0 {
				_ = c.Set(ctx, key, nullPlaceholder, opt.NullTTL)
			}
			return zero, loadErr
		}

		// 序列化并回写缓存
		encoded, encErr := json.Marshal(data)
		if encErr == nil {
			actualTTL := JitterTTL(opt.TTL, 10)
			_ = c.Set(ctx, key, encoded, actualTTL)
		}
		return data, nil
	}

	if opt.DisableSingleflight {
		return executeLoader()
	}

	// Singleflight 合并并发请求
	ch := sfGroup.DoChan(key, func() (any, error) {
		return executeLoader()
	})

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return zero, res.Err
		}
		val, ok := res.Val.(T)
		if !ok {
			return zero, fmt.Errorf("cache: unexpected singleflight result type %T", res.Val)
		}
		return val, nil
	}
}

// isNotFoundError 判断错误是否属于“资源未找到”（如 sql.ErrNoRows 或 gorm.ErrRecordNotFound）
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return msg == "record not found" || msg == "sql: no rows in result set"
}
