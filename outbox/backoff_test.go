package outbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fitan/fxkit/outbox"
)

type alwaysFailPublisher struct {
	mu    sync.Mutex
	calls int
}

func (p *alwaysFailPublisher) PublishEvent(context.Context, string, string, []byte, map[string]string) error {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return errors.New("downstream down")
}

func (p *alwaysFailPublisher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestRelay_BackoffDelaysRetry 回归：失败后写入 next_attempt_at，未到期不会被认领。
func TestRelay_BackoffDelaysRetry(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()

	cfg := mustOutboxCfg(t)
	cfg.BatchSize = 1
	cfg.MaxRetries = 10
	cfg.RetryBase = time.Hour
	cfg.RetryJitterPercent = 0

	row := outbox.OutboxEvent{
		Pubsub:  "pubsub",
		Topic:   "evt",
		Payload: []byte(`{"k":"v"}`),
		Status:  outbox.StatusPending,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: cfg})
	pub := &alwaysFailPublisher{}
	relay.SetPublisher(pub)

	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if pub.callCount() != 1 {
		t.Fatalf("calls=%d, want 1", pub.callCount())
	}

	var got outbox.OutboxEvent
	if err := client.Conn(ctx).First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.NextAttemptAt == nil || !got.NextAttemptAt.After(time.Now()) {
		t.Fatalf("next_attempt_at=%v, want future time", got.NextAttemptAt)
	}

	// 未到期：不应被再次认领/投递。
	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if pub.callCount() != 1 {
		t.Fatalf("calls=%d, want 1 (row must not be retried before next_attempt_at)", pub.callCount())
	}

	// 手动把重试时间拨到过去，验证可以再次投递。
	if err := client.Conn(ctx).Model(&outbox.OutboxEvent{}).
		Where("id = ?", row.ID).
		Update("next_attempt_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	var after outbox.OutboxEvent
	if err := client.Conn(ctx).First(&after, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	t.Logf("after update: status=%s attempts=%d next=%v", after.Status, after.Attempts, after.NextAttemptAt)
	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if pub.callCount() != 2 {
		t.Fatalf("calls=%d, want 2 after backoff elapsed", pub.callCount())
	}
}
