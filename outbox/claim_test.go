package outbox_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fitan/fxkit/outbox"
)

func mustOutboxCfg(t *testing.T) *outbox.Config {
	t.Helper()
	c := &outbox.Config{}
	c.SetDefaults()
	return c
}

func TestClaim_SingleBatchClaimsAll(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		row := outbox.OutboxEvent{
			Pubsub:  "pubsub",
			Topic:   "user-created",
			Payload: fmt.Appendf(nil, `{"i":%d}`, i),
			Status:  outbox.StatusPending,
		}
		if err := client.Conn(ctx).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: mustOutboxCfg(t)})
	pub := &mockPublisher{}
	relay.SetPublisher(pub)

	n, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("expected 5 processed, got %d", n)
	}
	if len(pub.calls) != 5 {
		t.Fatalf("expected 5 publishes, got %d", len(pub.calls))
	}
}

func TestClaim_NoDoublePublish(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()

	row := outbox.OutboxEvent{
		Pubsub:  "pubsub",
		Topic:   "user-created",
		Payload: []byte(`{"user_id":"1"}`),
		Status:  outbox.StatusPending,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: mustOutboxCfg(t)})
	pub := &mockPublisher{}
	relay.SetPublisher(pub)

	n1, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n1 != 1 || n2 != 0 {
		t.Fatalf("expected n1=1 n2=0, got n1=%d n2=%d", n1, n2)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(pub.calls))
	}
}

func TestClaim_ReclaimsStaleProcessing(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()

	stale := time.Now().UTC().Add(-time.Hour)
	row := outbox.OutboxEvent{
		Pubsub:   "pubsub",
		Topic:    "user-created",
		Payload:  []byte(`{"user_id":"stale"}`),
		Status:   outbox.StatusProcessing,
		LockedAt: &stale,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: mustOutboxCfg(t)})
	pub := &mockPublisher{}
	relay.SetPublisher(pub)

	n, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected stale processing to be reclaimed, got n=%d", n)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(pub.calls))
	}

	var updated outbox.OutboxEvent
	if err := client.Conn(ctx).First(&updated, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Status != outbox.StatusPublished {
		t.Fatalf("expected published, got %q", updated.Status)
	}
	if updated.LockedAt != nil {
		t.Fatalf("expected locked_at cleared, got %v", updated.LockedAt)
	}
}

func TestClaim_DoesNotReclaimFreshProcessing(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()

	fresh := time.Now().UTC()
	row := outbox.OutboxEvent{
		Pubsub:   "pubsub",
		Topic:    "user-created",
		Payload:  []byte(`{"user_id":"fresh"}`),
		Status:   outbox.StatusProcessing,
		LockedAt: &fresh,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: mustOutboxCfg(t)})
	pub := &mockPublisher{}
	relay.SetPublisher(pub)

	n, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected fresh processing not reclaimed, got n=%d", n)
	}
	if len(pub.calls) != 0 {
		t.Fatalf("expected 0 publishes, got %d", len(pub.calls))
	}

	var updated outbox.OutboxEvent
	if err := client.Conn(ctx).First(&updated, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Status != outbox.StatusProcessing {
		t.Fatalf("expected still processing, got %q", updated.Status)
	}
}

func TestClaim_ReclaimsProcessingWithNilLockedAt(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()

	// Legacy / crash mid-claim rows may lack locked_at; treat as reclaimable.
	row := outbox.OutboxEvent{
		Pubsub:  "pubsub",
		Topic:   "user-created",
		Payload: []byte(`{"user_id":"orphan"}`),
		Status:  outbox.StatusProcessing,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: mustOutboxCfg(t)})
	pub := &mockPublisher{}
	relay.SetPublisher(pub)

	n, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected nil locked_at processing to be reclaimed, got n=%d", n)
	}
}
