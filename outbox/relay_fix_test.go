package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fitan/fxkit/outbox"
	"gorm.io/gorm"
)

// singleConn 让 sqlite :memory: 只用一个连接（每个连接是独立的库）。
func singleConn(t *testing.T, client interface{ Pool() *gorm.DB }) {
	t.Helper()
	sqlDB, err := client.Pool().DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
}

// activePublisher 记录最大并发数。
type activePublisher struct {
	mu     sync.Mutex
	active int
	max    int
	delay  time.Duration
}

func (p *activePublisher) PublishEvent(ctx context.Context, _, _ string, _ []byte, _ map[string]string) error {
	p.mu.Lock()
	p.active++
	if p.active > p.max {
		p.max = p.active
	}
	p.mu.Unlock()

	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
	}

	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return nil
}

// TestRelay_PartitionedConcurrencyIsCapped 回归：
// publish_concurrency 是上限，不能因为分组数多就放大成「每分组一个 worker」。
func TestRelay_PartitionedConcurrencyIsCapped(t *testing.T) {
	client := testDB(t)
	singleConn(t, client)
	ctx := context.Background()

	cfg := mustOutboxCfg(t)
	cfg.BatchSize = 8
	cfg.PublishConcurrency = 2
	cfg.PartitionField = "key"
	cfg.MaxRetries = 3
	cfg.PublishTimeout = 5 * time.Second

	for i := 0; i < 8; i++ {
		row := outbox.OutboxEvent{
			Pubsub:  "pubsub",
			Topic:   "evt",
			Payload: fmt.Appendf(nil, `{"key":"k%d"}`, i),
			Status:  outbox.StatusPending,
		}
		if err := client.Conn(ctx).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: cfg})
	pub := &activePublisher{delay: 30 * time.Millisecond}
	relay.SetPublisher(pub)

	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if pub.max > cfg.PublishConcurrency {
		t.Fatalf("max concurrency=%d, want <= %d", pub.max, cfg.PublishConcurrency)
	}
}

// seqPublisher 记录调用顺序并对指定 seq 返回错误。
type seqPublisher struct {
	mu   sync.Mutex
	data []string
}

func (p *seqPublisher) PublishEvent(_ context.Context, _, _ string, data []byte, _ map[string]string) error {
	p.mu.Lock()
	p.data = append(p.data, string(data))
	p.mu.Unlock()
	if strings.Contains(string(data), `"seq":1`) {
		return errors.New("boom")
	}
	return nil
}

// TestRelay_PartitionFieldDefaultConcurrencyStopsOnFailure 回归：
// PublishConcurrency 默认是 1。只设 partition_field 时，同 key 失败也必须停住该组，
// 其他 key 仍要发出。
func TestRelay_PartitionFieldDefaultConcurrencyStopsOnFailure(t *testing.T) {
	client := testDB(t)
	singleConn(t, client)
	ctx := context.Background()

	cfg := mustOutboxCfg(t)
	cfg.BatchSize = 10
	cfg.PublishConcurrency = 1
	cfg.PartitionField = "key"
	cfg.MaxRetries = 3

	for _, payload := range []string{
		`{"key":"a","seq":1}`,
		`{"key":"b","seq":1}`,
		`{"key":"a","seq":2}`,
	} {
		row := outbox.OutboxEvent{
			Pubsub:  "pubsub",
			Topic:   "evt",
			Payload: []byte(payload),
			Status:  outbox.StatusPending,
		}
		if err := client.Conn(ctx).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: cfg})
	pub := &failKeyPublisher{fail: `"key":"a","seq":1`}
	relay.SetPublisher(pub)

	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	sawB := false
	for _, d := range pub.data {
		if strings.Contains(d, `"key":"a","seq":2`) {
			t.Fatalf("same-key event was published after failure: %v", pub.data)
		}
		if strings.Contains(d, `"key":"b"`) {
			sawB = true
		}
	}
	if !sawB {
		t.Fatalf("other key should still publish when concurrency is 1: %v", pub.data)
	}
}

// failKeyPublisher 记录投递，并只让包含 fail 子串的 payload 失败。
type failKeyPublisher struct {
	mu   sync.Mutex
	data []string
	fail string
}

func (p *failKeyPublisher) PublishEvent(_ context.Context, _, _ string, data []byte, _ map[string]string) error {
	p.mu.Lock()
	p.data = append(p.data, string(data))
	p.mu.Unlock()
	if strings.Contains(string(data), p.fail) {
		return errors.New("boom")
	}
	return nil
}

// TestRelay_PerKeyOrderStopsOnFailure 回归：
// 同 key 的前一条失败后，后续事件不能在本次批次里先被投递。
func TestRelay_PerKeyOrderStopsOnFailure(t *testing.T) {
	client := testDB(t)
	singleConn(t, client)
	ctx := context.Background()

	cfg := mustOutboxCfg(t)
	cfg.BatchSize = 10
	cfg.PublishConcurrency = 2
	cfg.PartitionField = "key"
	cfg.MaxRetries = 3

	for _, seq := range []int{1, 2} {
		row := outbox.OutboxEvent{
			Pubsub:  "pubsub",
			Topic:   "evt",
			Payload: fmt.Appendf(nil, `{"key":"a","seq":%d}`, seq),
			Status:  outbox.StatusPending,
		}
		if err := client.Conn(ctx).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: cfg})
	pub := &seqPublisher{}
	relay.SetPublisher(pub)

	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	for _, d := range pub.data {
		if strings.Contains(d, `"seq":2`) {
			t.Fatalf("event seq=2 was published before seq=1 succeeded: %v", pub.data)
		}
	}
}

// TestRelay_PublishTimeout 回归：卡住的 publisher 不能无限阻塞批次。
func TestRelay_PublishTimeout(t *testing.T) {
	client := testDB(t)
	singleConn(t, client)
	ctx := context.Background()

	cfg := mustOutboxCfg(t)
	cfg.BatchSize = 1
	cfg.PublishTimeout = 50 * time.Millisecond
	cfg.MaxRetries = 3

	row := outbox.OutboxEvent{
		Pubsub:  "pubsub",
		Topic:   "evt",
		Payload: []byte(`{"key":"a"}`),
		Status:  outbox.StatusPending,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: cfg})
	relay.SetPublisher(blockingPublisher{})

	start := time.Now()
	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ProcessBatch blocked for %s despite publish_timeout", elapsed)
	}
}

type blockingPublisher struct{}

func (blockingPublisher) PublishEvent(ctx context.Context, _, _ string, _ []byte, _ map[string]string) error {
	<-ctx.Done()
	return ctx.Err()
}

// TestRelay_RestartAfterStop 回归：Stop 后必须能再次 Start（早期 cancel 不重置，重启静默失效）。
func TestRelay_RestartAfterStop(t *testing.T) {
	client := testDB(t)
	singleConn(t, client)
	ctx := context.Background()

	cfg := mustOutboxCfg(t)
	cfg.PollInterval = 5 * time.Millisecond
	cfg.BatchSize = 10

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: cfg})
	relay.SetPublisher(&activePublisher{})

	enqueue := func(topic string) uint64 {
		row := outbox.OutboxEvent{
			Pubsub:  "pubsub",
			Topic:   topic,
			Payload: []byte(`{"k":"v"}`),
			Status:  outbox.StatusPending,
		}
		if err := client.Conn(ctx).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	waitPublished := func(id uint64) {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var row outbox.OutboxEvent
			if err := client.Conn(ctx).First(&row, id).Error; err == nil && row.Status == outbox.StatusPublished {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("event %d was not published", id)
	}

	first := enqueue("one")
	if err := relay.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitPublished(first)
	relay.Stop()

	second := enqueue("two")
	if err := relay.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitPublished(second)
	relay.Stop()
}
