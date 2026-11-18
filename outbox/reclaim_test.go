package outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/fitan/fxkit/outbox"
)

// TestRelay_ReclaimDoesNotConsumeRetryBudget 回归：
// 租约过期后被重新认领只是换人执行，不应把 attempts 计为一次业务失败。
func TestRelay_ReclaimDoesNotConsumeRetryBudget(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-time.Hour)
	row := outbox.OutboxEvent{
		Pubsub:   "pubsub",
		Topic:    "evt",
		Payload:  []byte(`{"k":"v"}`),
		Status:   outbox.StatusProcessing,
		LockedAt: &old,
		LeaseID:  "stale-lease",
		Attempts: 5,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	cfg := mustOutboxCfg(t)
	cfg.BatchSize = 10
	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: cfg})
	relay.SetPublisher(&activePublisher{})
	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}

	var got outbox.OutboxEvent
	if err := client.Conn(ctx).First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != outbox.StatusPublished {
		t.Fatalf("status=%q, want published", got.Status)
	}
	if got.Attempts != 5 {
		t.Fatalf("attempts=%d, reclaim must not consume retry budget", got.Attempts)
	}
}
