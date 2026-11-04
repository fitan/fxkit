package outbox_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/outbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func regressionDB(t *testing.T) *gormx.Client {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared&_busy_timeout=5000"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&outbox.OutboxEvent{}))
	return gormx.NewTestClient(db)
}

// failingPublisher 永远失败，用于验证 relay 不会在失败批上空转。
type failingPublisher struct {
	calls atomic.Int64
}

func (p *failingPublisher) PublishEvent(context.Context, string, string, []byte, map[string]string) error {
	p.calls.Add(1)
	return errors.New("broker unavailable")
}

// TestRelayRunLoopDoesNotHotLoopOnPersistentFailure 是回归测试：
// 早期 runLoop 用「Claimed >= BatchSize」判断是否立刻继续，而失败行会被放回
// pending 并再次被领走，导致「满批领取 → 全部失败 → 立刻重领」的 CPU 热循环。
func TestRelayRunLoopDoesNotHotLoopOnPersistentFailure(t *testing.T) {
	client := regressionDB(t)
	ctx := context.Background()

	store := outbox.NewStore(client)
	const batch = 5
	for i := 0; i < batch; i++ {
		require.NoError(t, store.Enqueue(ctx, outbox.Message{Topic: "t", Payload: []byte(`{}`)}))
	}

	pub := &failingPublisher{}
	cfg := outbox.Config{
		Enabled:      true,
		PollInterval: 200 * time.Millisecond,
		BatchSize:    batch,
		MaxRetries:   1000, // 保持 pending，逼出热循环
		ClaimTimeout: 30 * time.Second,
	}
	require.NoError(t, cfg.Validate())

	r := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: &cfg, Publishers: []outbox.EventPublisher{pub}})
	require.NoError(t, r.Start(ctx))
	// 一个 poll 周期内，如果循环正确等待，调用次数应接近 batch（1 批）；
	// 热循环会达到数百次甚至更多。
	time.Sleep(120 * time.Millisecond)
	r.Stop()

	calls := pub.calls.Load()
	assert.Less(t, calls, int64(batch*5), "relay hot-looped: %d publish attempts in ~1 poll interval", calls)
}

// concurrencyPublisher 记录并发度峰值，用于验证 publish_concurrency。
type concurrencyPublisher struct {
	calls   atomic.Int64
	current atomic.Int64
	maxConc atomic.Int64
	delay   time.Duration
}

func (p *concurrencyPublisher) PublishEvent(context.Context, string, string, []byte, map[string]string) error {
	p.calls.Add(1)
	cur := p.current.Add(1)
	for {
		m := p.maxConc.Load()
		if cur <= m || p.maxConc.CompareAndSwap(m, cur) {
			break
		}
	}
	time.Sleep(p.delay)
	p.current.Add(-1)
	return nil
}

func TestProcessBatchPublishConcurrency(t *testing.T) {
	// 并发写必须用文件库 + WAL：shared-cache 内存库是表级锁，
	// 无法靠 busy_timeout 消除 SQLITE_LOCKED（真实 PG/MySQL 不存在该问题）。
	dsn := "file:" + filepath.Join(t.TempDir(), "outbox.db") + "?_busy_timeout=5000&_journal_mode=WAL"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&outbox.OutboxEvent{}))
	client := gormx.NewTestClient(db)

	ctx := context.Background()
	store := outbox.NewStore(client)

	const n = 8
	for i := 0; i < n; i++ {
		require.NoError(t, store.Enqueue(ctx, outbox.Message{Topic: "t", Payload: []byte(`{}`)}))
	}

	pub := &concurrencyPublisher{delay: 20 * time.Millisecond}
	cfg := outbox.Config{
		Enabled:            true,
		PollInterval:       time.Second,
		BatchSize:          n,
		MaxRetries:         3,
		ClaimTimeout:       30 * time.Second,
		PublishConcurrency: 4,
	}
	require.NoError(t, cfg.Validate())
	r := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: &cfg, Publishers: []outbox.EventPublisher{pub}})

	got, err := r.ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, n, got)
	assert.Equal(t, int64(n), pub.calls.Load())
	assert.Greater(t, pub.maxConc.Load(), int64(1), "expected parallel publish with publish_concurrency=4")
}
