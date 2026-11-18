package cachex

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// Options 针对单次 GetOrSet 操作的高级配置。
type Options struct {
	// TTL 过期时间。语义只有三种：
	//   >0  使用该值
	//   =0  使用模块默认（[defaultOptions] 中 30m）
	//   <0  永不过期
	//
	// 早期这里还有并列的 Forever bool，「永不过期」有两种表达；
	// 且 TTL<=0 又兼顾「用默认」，语义重叠。现在合并为一个字段。
	TTL time.Duration
	// DisableSingleflight 临时禁用该 key 的 singleflight 并发合并。
	DisableSingleflight bool
	// NullTTL 空值（「未找到」）缓存时间。>0 生效；<=0 表示不缓存空值。
	NullTTL time.Duration
	// JitterMaxPercent 过期时间抖动百分比上限（0-50）。默认 10。
	JitterMaxPercent int
	// LoadTimeout 为 loader 附加的独立超时（0 表示不额外限制）。
	//
	// singleflight 共享的 loader 会脱离首个调用者的取消信号执行（见
	// [GetOrSet]），此超时用于避免 loader 无限期运行。
	LoadTimeout time.Duration
}

// foreverTTL 是 Options.TTL 的哨兵值：永不过期。
const foreverTTL time.Duration = -1

// Option 定义 Options 修改器函数。
type Option func(*Options)

// WithTTL 指定单次缓存过期时间。ttl=0 表示用模块默认。
func WithTTL(ttl time.Duration) Option {
	return func(o *Options) { o.TTL = ttl }
}

// WithForever 指定缓存永不过期。
func WithForever() Option {
	return func(o *Options) { o.TTL = foreverTTL }
}

// WithoutSingleflight 显式禁用 singleflight。
func WithoutSingleflight() Option {
	return func(o *Options) {
		o.DisableSingleflight = true
	}
}

// WithoutCacheNull 禁用空值缓存（等价于 WithNullTTL(0)）。
func WithoutCacheNull() Option {
	return func(o *Options) { o.NullTTL = 0 }
}

// WithNullTTL 指定空值缓存时间。
func WithNullTTL(ttl time.Duration) Option {
	return func(o *Options) {
		o.NullTTL = ttl
	}
}

// WithJitter 指定 TTL 抖动百分比（0-50）。
func WithJitter(maxPercent int) Option {
	return func(o *Options) {
		o.JitterMaxPercent = maxPercent
	}
}

// WithLoadTimeout 为 loader 指定独立超时，避免共享 singleflight 的加载无限期运行。
func WithLoadTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.LoadTimeout = timeout
	}
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

// errNilLoader 表示 GetOrSet 调用没有提供 loader。
var errNilLoader = errors.New("cache: loader cannot be nil")

// GetOrSet 是通用的泛型 Cache-Aside 模式辅助函数。
// 优先从 cache 获取并反序列化为 T；若缓存未命中，则执行 loader 从数据源加载，序列化后写入 cache。
// 内置 Singleflight（防击穿）、空值缓存（防穿透）及 TTL 抖动（防雪崩）。
//
// 命中空值缓存时返回 [NegativeHitError]，errors.Is(err, ErrNegativeHit)。
// loader 返回 fxerrors.NotFound 时，命中结果仍是 KindNotFound（Huma 404）。
// gorm / database/sql / pgx 的未找到第一次经 Wrap 是 500，命中后也是 500。
//
// singleflight 合并的 loader 使用 context.WithoutCancel(ctx) 执行，
// 首个调用者取消不会波及其它等待者；每个等待者仍可因自己的 ctx 取消而提前返回。
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
			return zero, errNilLoader
		}
		return loader(ctx)
	}

	opt := defaultOptions()
	for _, fn := range opts {
		if fn != nil {
			fn(&opt)
		}
	}
	normalizeOptions(&opt)

	// 1. 尝试从缓存读取
	valBytes, err := c.Get(ctx, key)
	switch {
	case err == nil:
		if neg, ok := decodeNegative(valBytes, key); ok {
			// 命中空值占位：与首次穿透时的错误语义保持一致。
			recordLookup(ctx, "negative")
			return zero, neg
		}
		if target, ok := decodeValue[T](valBytes); ok {
			recordLookup(ctx, "hit")
			return target, nil
		}
		// 反序列化失败或类型不匹配视作脏缓存，继续走 loader 覆盖。
		recordLookup(ctx, "miss")
	case errors.Is(err, ErrCacheMiss):
		// 正常未命中，继续。
		recordLookup(ctx, "miss")
	case errors.Is(err, ErrCacheDisabled):
		// 缓存未启用：继续回源。仍走下面的 singleflight —— 「没有缓存」
		// 不应该顺带变成「没有防击穿」。
		recordLookup(ctx, "disabled")
	default:
		// 缓存后端故障（网络等）：降级为回源，但同样经过 singleflight。
		// 否则 Redis 抖动时并发压力最大的那一刻，恰好所有请求会同时打到数据源。
		recordLookup(ctx, "error")
	}

	// 2. 执行 loader 加载（带 Singleflight 防击穿）
	executeLoader := func(loadCtx context.Context) (out T, loadErr error) {
		if loader == nil {
			return zero, errNilLoader
		}
		start := time.Now()
		defer func() { recordLoader(ctx, time.Since(start)) }()

		loadCtx, span := startLoadSpan(loadCtx)
		defer func() { endLoadSpan(span, loadErr) }()

		if opt.LoadTimeout > 0 {
			var cancel context.CancelFunc
			loadCtx, cancel = context.WithTimeout(loadCtx, opt.LoadTimeout)
			defer cancel()
		}

		data, loadErr := loader(loadCtx)
		if loadErr != nil {
			// 如果启用空值缓存且属于「未找到」，写入负缓存防穿透。
			// TTL 同样加抖动：否则一批 404 会同时过期、同时回源。
			if opt.NullTTL > 0 && isNotFoundError(loadErr) {
				nullTTL := JitterTTL(opt.NullTTL, opt.JitterMaxPercent)
				_ = c.Set(loadCtx, key, encodeNegative(loadErr), nullTTL)
			}
			return zero, loadErr
		}

		// 序列化并回写缓存
		encoded, encErr := encodeValue(data)
		if encErr == nil {
			ttl := opt.TTL
			if ttl < 0 {
				ttl = 0 // Cache.Set 约定：ttl <= 0 表示永不过期
			} else {
				ttl = JitterTTL(ttl, opt.JitterMaxPercent)
			}
			_ = c.Set(loadCtx, key, encoded, ttl)
		}
		return data, nil
	}

	if opt.DisableSingleflight {
		return executeLoader(ctx)
	}

	// Singleflight 合并并发请求（按 cache 实例 + 泛型类型隔离）。
	// 使用 WithoutCancel 执行 loader：首个调用者取消不会让其它等待者收到
	// context canceled，也不会导致结果不落缓存。
	flightKey := typeFingerprint[T]() + "\x00" + key
	group, keyPrefix := singleflightGroup(c)
	ch := group.DoChan(keyPrefix+flightKey, func() (any, error) {
		return executeLoader(context.WithoutCancel(ctx))
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

// normalizeOptions 统一单次调用选项的边界值。
//
// jitter 上限不在这里夹：JitterTTL 自己会夹（它是公开 API，也会被直接调用）。
func normalizeOptions(opt *Options) {
	def := defaultOptions()
	if opt.TTL == 0 {
		opt.TTL = def.TTL
	}
	if opt.NullTTL < 0 {
		opt.NullTTL = 0 // 负值一律视为关闭
	}
	if opt.JitterMaxPercent < 0 {
		opt.JitterMaxPercent = 0
	}
}

// defaultOptions 返回与 [Config.SetDefaults] 一致的默认值。
func defaultOptions() Options {
	return Options{
		TTL:              30 * time.Minute,
		NullTTL:          1 * time.Minute,
		JitterMaxPercent: 10,
	}
}

// OptionsFromConfig 把模块级配置映射为单次操作的默认 [Options]。
// 之前 GetOrSet 完全忽略 Config 的 default_ttl / null_ttl / singleflight /
// jitter_max_percent，改 yaml 不生效；这里提供显式桥接，并由 [Resolver] 使用。
func OptionsFromConfig(cfg *Config) Options {
	opt := defaultOptions()
	if cfg == nil {
		return opt
	}
	// 未经 SetDefaults 的 Config（代码里直接构造的零值/半零值）视为「未配置」：
	// 否则 NullTTL=0 且 Singleflight=false 会让负缓存与单飞静默失效。
	//
	// 早期实现用「四个字段全为零」猜，会误伤显式配置：
	// `null_ttl: 0` + `singleflight: false` 恰好让四个字段全为零，整段配置被忽略。
	// 现在由 SetDefaults 打标记，语义明确。
	if !cfg.defaultsApplied {
		return opt
	}
	if cfg.DefaultTTL > 0 {
		opt.TTL = cfg.DefaultTTL
	}
	if cfg.NullTTL >= 0 {
		opt.NullTTL = cfg.NullTTL
	}
	opt.DisableSingleflight = !cfg.Singleflight
	if cfg.JitterMaxPercent > 0 {
		opt.JitterMaxPercent = cfg.JitterMaxPercent
	}
	return opt
}
