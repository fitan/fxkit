package cachex

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// sfGroups 为每个 Cache 实例维护独立的 singleflight group。
//
// 早期实现用包级全局 group、且只用业务 key 做 singleflight key，
// 导致「不同 cache 实例/租户/环境使用同一个 key 时结果互相串」。
// 现在按 cache 实例隔离（sync.Map 的 key 是 Cache 指针）。
var sfGroups = newSingleflightRegistry()

// Options 针对单次 GetOrSet 操作的高级配置。
type Options struct {
	// TTL 单次缓存过期时间。若 <= 0 则使用 DefaultTTL，而不是「永不过期」。
	// 如需永不过期，请使用 [WithForever]。
	TTL time.Duration
	// Forever 显式要求永不过期（忽略 TTL）。请使用 [WithForever] 设置。
	Forever bool
	// DisableSingleflight 临时禁用该 key 的 singleflight 并发合并。
	DisableSingleflight bool
	// CacheNull 是否缓存空值（当 loader 返回「未找到」类错误时）。默认 true。
	CacheNull bool
	// NullTTL 空值缓存时间。若 < 0 则使用默认值（1 分钟）；
	// 显式设为 0 表示不缓存空值（与 [Config.NullTTL] 文档一致）。
	NullTTL time.Duration
	// JitterMaxPercent 过期时间抖动百分比上限（0-50）。默认 10。
	JitterMaxPercent int
	// LoadTimeout 为 loader 附加的独立超时（0 表示不额外限制）。
	//
	// singleflight 共享的 loader 会脱离首个调用者的取消信号执行（见
	// [GetOrSet]），此超时用于避免 loader 无限期运行。
	LoadTimeout time.Duration
}

// Option 定义 Options 修改器函数。
type Option func(*Options)

// WithTTL 指定单次缓存过期时间。
//
// ttl <= 0 表示「使用默认 TTL」（[defaultOptions] 中为 30 分钟），
// 而不是永不过期；永不过期请使用 [WithForever]。
func WithTTL(ttl time.Duration) Option {
	return func(o *Options) {
		o.TTL = ttl
		o.Forever = false
	}
}

// WithForever 显式指定缓存永不过期。
func WithForever() Option {
	return func(o *Options) {
		o.Forever = true
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
// 命中空值缓存时返回 [NegativeHitError]，它同时满足
// errors.Is(err, ErrNegativeHit) 以及首次 loader 返回的「未找到」语义
// （gorm.ErrRecordNotFound / sql.ErrNoRows / pgx.ErrNoRows /
// fxerrors.Is(err, fxerrors.KindNotFound)）。
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
	case errors.Is(err, ErrCacheDisabled):
		recordLookup(ctx, "disabled")
		if loader == nil {
			return zero, errNilLoader
		}
		return loader(ctx)
	case errors.Is(err, ErrCacheMiss):
		// 正常未命中，继续。
		recordLookup(ctx, "miss")
	default:
		// 缓存后端故障（网络等）：降级为直连 loader，不阻断业务。
		recordLookup(ctx, "error")
		if loader == nil {
			return zero, errNilLoader
		}
		return loader(ctx)
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
			if opt.CacheNull && opt.NullTTL > 0 && isNotFoundError(loadErr) {
				_ = c.Set(loadCtx, key, encodeNegative(loadErr), opt.NullTTL)
			}
			return zero, loadErr
		}

		// 序列化并回写缓存
		encoded, encErr := encodeValue(data)
		if encErr == nil {
			ttl := opt.TTL
			if opt.Forever {
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
	ch := sfGroups.groupFor(c).DoChan(flightKey, func() (any, error) {
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
func normalizeOptions(opt *Options) {
	def := defaultOptions()
	if !opt.Forever && opt.TTL <= 0 {
		opt.TTL = def.TTL
	}
	if opt.NullTTL < 0 {
		opt.NullTTL = def.NullTTL
	}
	if opt.JitterMaxPercent < 0 {
		opt.JitterMaxPercent = 0
	}
	if opt.JitterMaxPercent > 50 {
		opt.JitterMaxPercent = 50
	}
}

// defaultOptions 返回与 [Config.SetDefaults] 一致的默认值。
func defaultOptions() Options {
	return Options{
		TTL:              30 * time.Minute,
		CacheNull:        true,
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
	// 零值 Config（直接构造/测试）视为「未配置」：否则 NullTTL=0 且
	// Singleflight=false 会让负缓存与单飞静默失效（与 yaml 路径行为不一致）。
	if cfg.DefaultTTL == 0 && cfg.NullTTL == 0 && cfg.JitterMaxPercent == 0 && !cfg.Singleflight {
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
