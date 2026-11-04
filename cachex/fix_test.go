package cachex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// recordingCache 记录 Set 时使用的 TTL，用于验证 TTL 归一化。
type recordingCache struct {
	MemoryCache
	setTTL time.Duration
	sets   int
}

func newRecordingCache() *recordingCache {
	return &recordingCache{MemoryCache: *NewMemoryCache()}
}

func (c *recordingCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	c.setTTL = ttl
	c.sets++
	return c.MemoryCache.Set(ctx, key, val, ttl)
}

func TestGetOrSet_NonPositiveTTLUsesDefault(t *testing.T) {
	ctx := context.Background()
	def := defaultOptions().TTL

	for _, ttl := range []time.Duration{0, -time.Second} {
		c := newRecordingCache()
		_, err := GetOrSet(ctx, c, fmt.Sprintf("k-%v", ttl), func(context.Context) (string, error) {
			return "v", nil
		}, WithTTL(ttl), WithJitter(0))
		require.NoError(t, err)
		assert.Equal(t, def, c.setTTL, "ttl=%v must fall back to default TTL, not never-expire", ttl)
	}
}

func TestGetOrSet_WithForeverWritesZeroTTL(t *testing.T) {
	ctx := context.Background()
	c := newRecordingCache()
	_, err := GetOrSet(ctx, c, "forever", func(context.Context) (string, error) {
		return "v", nil
	}, WithForever(), WithJitter(10))
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), c.setTTL, "WithForever must use ttl=0 (never expire)")

	// 缓存确实永不过期。
	val, err := c.Get(ctx, "forever")
	require.NoError(t, err)
	assert.Contains(t, string(val), `"v"`)
}

func TestNormalizeRedisTTL(t *testing.T) {
	assert.Equal(t, time.Duration(0), normalizeRedisTTL(-time.Second))
	assert.Equal(t, time.Duration(0), normalizeRedisTTL(0))
	assert.Equal(t, time.Minute, normalizeRedisTTL(time.Minute))
}

func TestNegativeCache_PreservesNotFoundSemantics(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name            string
		loadErr         error
		wantGorm        bool
		wantSQL         bool
		wantPgx         bool
		wantFxNotFound  bool
		wantPlainErrors bool
	}{
		{"gorm", gorm.ErrRecordNotFound, true, false, false, true, true},
		{"gorm-wrapped", fmt.Errorf("db: %w", gorm.ErrRecordNotFound), true, false, false, true, true},
		{"sql", sql.ErrNoRows, false, true, false, false, true},
		{"pgx", pgx.ErrNoRows, false, true, true, false, true},
		{"fxerrors", fxerrors.NotFound("user", "id=%d", 1), false, false, false, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := NewMemoryCache()
			var loads int64
			loader := func(context.Context) (string, error) {
				atomic.AddInt64(&loads, 1)
				return "", tc.loadErr
			}

			_, first := GetOrSet(ctx, mem, "k", loader, WithNullTTL(time.Minute))
			require.Error(t, first)
			assert.False(t, errors.Is(first, ErrNegativeHit))

			_, hit := GetOrSet(ctx, mem, "k", func(context.Context) (string, error) {
				atomic.AddInt64(&loads, 1)
				return "unexpected", nil
			}, WithNullTTL(time.Minute))
			require.Error(t, hit)
			assert.Equal(t, int64(1), atomic.LoadInt64(&loads))

			assert.True(t, errors.Is(hit, ErrNegativeHit), "negative hit sentinel")
			assert.True(t, IsNegativeHit(hit), "legacy helper")
			assert.Equal(t, tc.wantGorm, errors.Is(hit, gorm.ErrRecordNotFound), "gorm sentinel")
			assert.Equal(t, tc.wantSQL, errors.Is(hit, sql.ErrNoRows), "sql sentinel")
			assert.Equal(t, tc.wantPgx, errors.Is(hit, pgx.ErrNoRows), "pgx sentinel")
			assert.Equal(t, tc.wantFxNotFound, fxerrors.Is(hit, fxerrors.KindNotFound), "fxerrors kind")

			var fxe *fxerrors.Error
			if tc.wantFxNotFound {
				require.True(t, errors.As(hit, &fxe))
				assert.Equal(t, fxerrors.KindNotFound, fxe.Kind)
			}
		})
	}
}

func TestClassifyNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"gorm", gorm.ErrRecordNotFound, true},
		{"gorm-wrapped", fmt.Errorf("query: %w", gorm.ErrRecordNotFound), true},
		{"sql", sql.ErrNoRows, true},
		{"pgx", pgx.ErrNoRows, true},
		{"fxerrors", fxerrors.NotFound("order", ""), true},
		{"legacy-text", errors.New("record not found"), true},
		{"legacy-sql-text", errors.New("sql: no rows in result set"), true},
		{"other", errors.New("connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isNotFoundError(tc.err))
		})
	}
}

// TestGetOrSet_SingleflightDetachesCallerContext：首个调用者的 ctx 取消后，
// 其它等待者不应收到 context canceled，loader 的结果仍应写入缓存。
func TestGetOrSet_SingleflightDetachesCallerContext(t *testing.T) {
	c := NewMemoryCache()
	started := make(chan struct{})
	release := make(chan struct{})

	var loads int64
	var loaderCtxCanceled atomic.Bool
	loader := func(ctx context.Context) (string, error) {
		atomic.AddInt64(&loads, 1)
		close(started)
		<-release
		loaderCtxCanceled.Store(ctx.Err() != nil)
		return "value", nil
	}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := GetOrSet(firstCtx, c, "k", loader)
		firstDone <- err
	}()

	<-started
	cancelFirst()
	require.ErrorIs(t, <-firstDone, context.Canceled, "the canceled caller should return early")

	secondDone := make(chan struct {
		val string
		err error
	}, 1)
	go func() {
		v, err := GetOrSet(context.Background(), c, "k", loader)
		secondDone <- struct {
			val string
			err error
		}{v, err}
	}()
	// 给第二个调用者一点时间加入同一个 flight。
	time.Sleep(20 * time.Millisecond)
	close(release)

	res := <-secondDone
	require.NoError(t, res.err)
	assert.Equal(t, "value", res.val)
	assert.Equal(t, int64(1), atomic.LoadInt64(&loads))
	assert.False(t, loaderCtxCanceled.Load(), "shared loader must not inherit the first caller's cancellation")

	// 结果已落缓存。
	var reloads int64
	v, err := GetOrSet(context.Background(), c, "k", func(context.Context) (string, error) {
		atomic.AddInt64(&reloads, 1)
		return "unexpected", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "value", v)
	assert.Equal(t, int64(0), atomic.LoadInt64(&reloads), "the flight result must be cached")
}

type flightUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type flightOrder struct {
	ID     int `json:"id"`
	Amount int `json:"amount"`
}

// TestGetOrSet_SameKeyDifferentTypes：相同 key、不同类型必须互不串数据。
func TestGetOrSet_SameKeyDifferentTypes(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache()

	u, err := GetOrSet(ctx, c, "same", func(context.Context) (flightUser, error) {
		return flightUser{ID: 1, Name: "alice"}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "alice", u.Name)

	var orderLoads int64
	o, err := GetOrSet(ctx, c, "same", func(context.Context) (flightOrder, error) {
		atomic.AddInt64(&orderLoads, 1)
		return flightOrder{ID: 2, Amount: 42}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&orderLoads), "type mismatch must trigger a reload, not silently return zero values")
	assert.Equal(t, flightOrder{ID: 2, Amount: 42}, o)

	// 相同 key 只有一个槽位：后写入的 Order 覆盖了 User 条目；
	// 但读取时类型不匹配必须回源，而不是把 Order 的 JSON 静默解成 User 零值。
	var orderReloads int64
	o2, err := GetOrSet(ctx, c, "same", func(context.Context) (flightOrder, error) {
		atomic.AddInt64(&orderReloads, 1)
		return flightOrder{}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), atomic.LoadInt64(&orderReloads), "order cache must hit")
	assert.Equal(t, flightOrder{ID: 2, Amount: 42}, o2)

	var userReloads int64
	u2, err := GetOrSet(ctx, c, "same", func(context.Context) (flightUser, error) {
		atomic.AddInt64(&userReloads, 1)
		return flightUser{ID: 1, Name: "alice"}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&userReloads), "type mismatch must reload")
	assert.Equal(t, flightUser{ID: 1, Name: "alice"}, u2)
}

// TestGetOrSet_SingleflightKeyIncludesType：不同类型不能合并到同一个 flight。
func TestGetOrSet_SingleflightKeyIncludesType(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache()
	started := make(chan struct{})
	release := make(chan struct{})
	userDone := make(chan struct{})

	go func() {
		defer close(userDone)
		_, _ = GetOrSet(ctx, c, "typed", func(context.Context) (flightUser, error) {
			close(started)
			<-release
			return flightUser{ID: 1, Name: "u"}, nil
		})
	}()
	<-started

	orderCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	o, err := GetOrSet(orderCtx, c, "typed", func(context.Context) (flightOrder, error) {
		return flightOrder{ID: 9, Amount: 7}, nil
	})
	require.NoError(t, err, "different type must not wait on the in-flight User loader")
	assert.Equal(t, flightOrder{ID: 9, Amount: 7}, o)

	close(release)
	<-userDone
}

func TestMemoryCache_DeletesExpiredOnGet(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache()
	require.NoError(t, c.Set(ctx, "expired", []byte("v"), 5*time.Millisecond))
	time.Sleep(15 * time.Millisecond)

	_, err := c.Get(ctx, "expired")
	assert.ErrorIs(t, err, ErrCacheMiss)
	assert.Equal(t, 0, c.Len(), "Get must delete the expired item")
}

func TestMemoryCache_SweepExpired(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache()
	for i := 0; i < 10; i++ {
		require.NoError(t, c.Set(ctx, fmt.Sprintf("k%d", i), []byte("v"), 5*time.Millisecond))
	}
	time.Sleep(15 * time.Millisecond)
	assert.Equal(t, 10, c.SweepExpired())
	assert.Equal(t, 0, c.Len())
}

func TestMemoryCache_LazySweepOnWrites(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache()
	require.NoError(t, c.Set(ctx, "expired", []byte("v"), 5*time.Millisecond))
	time.Sleep(15 * time.Millisecond)

	for i := 0; i < memorySweepEvery; i++ {
		require.NoError(t, c.Set(ctx, fmt.Sprintf("fresh-%d", i), []byte("v"), time.Hour))
	}
	assert.Less(t, c.Len(), memorySweepEvery+1, "write threshold should sweep expired keys")
}

func TestNewRedisCache_Close(t *testing.T) {
	c, err := New(&Config{
		Driver: "redis",
		Redis: RedisConfig{
			Addr: "127.0.0.1:1",
		},
	})
	require.NoError(t, err)

	closer, ok := c.(interface{ Close() error })
	require.True(t, ok, "module-created cache must be closable")
	require.NoError(t, closer.Close())

	rc, ok := c.(*RedisCache)
	require.True(t, ok)
	require.Error(t, rc.Client().Ping(context.Background()).Err(), "Ping after Close must fail")
}

// TestModuleClosesRedisClient：Module 注册的 OnStop 应关闭自己创建的 Redis 客户端。
func TestModuleClosesRedisClient(t *testing.T) {
	cfgYAML := `cache:
  driver: "redis"
  redis:
    addr: "127.0.0.1:1"
`
	tmp := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(tmp, []byte(cfgYAML), 0o600))

	cfg, err := config.New(config.Options{ConfigFile: tmp})
	require.NoError(t, err)

	injector := di.New()
	lc, err := di.Invoke[*di.Lifecycle](injector)
	require.NoError(t, err)
	di.Provide(injector, func(di.Injector) (*config.Config, error) {
		return cfg, nil
	})
	Module(injector)

	c, err := di.Invoke[Cache](injector)
	require.NoError(t, err)
	rc, ok := c.(*RedisCache)
	require.True(t, ok, "got %T", c)

	require.NoError(t, lc.Stop(context.Background()))
	require.Error(t, rc.Client().Ping(context.Background()).Err(), "Lifecycle OnStop should close the redis client")
}

// TestOptionsFromConfig_ZeroValueUsesDefaults 零值 Config 不能把负缓存与 singleflight 静默关掉。
func TestOptionsFromConfig_ZeroValueUsesDefaults(t *testing.T) {
	def := defaultOptions()
	for _, cfg := range []*Config{{}, {Driver: "memory"}} {
		opt := OptionsFromConfig(cfg)
		if opt.TTL != def.TTL || opt.NullTTL != def.NullTTL || opt.DisableSingleflight != def.DisableSingleflight {
			t.Fatalf("cfg=%+v got %+v want %+v", cfg, opt, def)
		}
	}
}
