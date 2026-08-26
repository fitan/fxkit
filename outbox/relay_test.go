package outbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fitan/fxkit/outbox"
)

var errPublish = errors.New("publish failed")

type mockPublisher struct {
	mu    sync.Mutex
	calls []publishCall
	err   error
}

type publishCall struct {
	Pubsub   string
	Topic    string
	Data     []byte
	Metadata map[string]string
}

func (m *mockPublisher) PublishEvent(_ context.Context, pubsubName, topicName string, data []byte, metadata map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, publishCall{Pubsub: pubsubName, Topic: topicName, Data: data, Metadata: metadata})
	return m.err
}

func TestRelay_ProcessBatch_PublishesAndMarksPublished(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()

	key := "evt-42"
	row := outbox.OutboxEvent{
		Pubsub:         "pubsub",
		Topic:          "user-created",
		Payload:        []byte(`{"user_id":"42"}`),
		IdempotencyKey: &key,
		Status:         outbox.StatusPending,
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
		t.Fatalf("expected 1 processed, got %d", n)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	if pub.calls[0].Topic != "user-created" {
		t.Fatalf("unexpected topic %q", pub.calls[0].Topic)
	}
	if pub.calls[0].Metadata[outbox.MetadataIdempotencyKey] != key {
		t.Fatalf("expected idempotency metadata, got %#v", pub.calls[0].Metadata)
	}

	var updated outbox.OutboxEvent
	if err := client.Conn(ctx).First(&updated, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Status != outbox.StatusPublished {
		t.Fatalf("expected published, got %q", updated.Status)
	}
	if updated.PublishedAt == nil {
		t.Fatal("expected published_at set")
	}
	if updated.LockedAt != nil {
		t.Fatalf("expected locked_at cleared after publish, got %v", updated.LockedAt)
	}
}

func TestRelay_ProcessBatch_PublishFailClearsLockAndRetries(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()

	row := outbox.OutboxEvent{
		Pubsub:  "pubsub",
		Topic:   "user-created",
		Payload: []byte(`{"user_id":"42"}`),
		Status:  outbox.StatusPending,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	oc := mustOutboxCfg(t)
	oc.MaxRetries = 3

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: oc})
	pub := &mockPublisher{err: errPublish}
	relay.SetPublisher(pub)

	n, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 published on fail, got %d", n)
	}

	var updated outbox.OutboxEvent
	if err := client.Conn(ctx).First(&updated, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Status != outbox.StatusPending {
		t.Fatalf("expected pending for retry, got %q", updated.Status)
	}
	if updated.Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", updated.Attempts)
	}
	if updated.LockedAt != nil {
		t.Fatalf("expected locked_at cleared after publish fail, got %v", updated.LockedAt)
	}

	pub.err = nil
	n, err = relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected retry publish, got n=%d", n)
	}
	if err := client.Conn(ctx).First(&updated, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Status != outbox.StatusPublished {
		t.Fatalf("expected published after retry, got %q", updated.Status)
	}
}

func TestRelay_StaleMarkFailedDoesNotResurrectPublished(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()

	row := outbox.OutboxEvent{
		Pubsub:  "pubsub",
		Topic:   "user-created",
		Payload: []byte(`{"user_id":"42"}`),
		Status:  outbox.StatusPending,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	pub := &callbackPublisher{fn: func() error {
		if err := client.Conn(ctx).Model(&outbox.OutboxEvent{}).Where("id = ?", row.ID).Updates(map[string]any{
			"status":       outbox.StatusPublished,
			"lease_id":     "other-replica",
			"locked_at":    nil,
			"published_at": time.Now().UTC(),
		}).Error; err != nil {
			t.Errorf("simulate other replica: %v", err)
		}
		return errPublish
	}}

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: mustOutboxCfg(t)})
	relay.SetPublisher(pub)
	n, err := relay.ProcessBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 published from this relay, got %d", n)
	}

	var got outbox.OutboxEvent
	if err := client.Conn(ctx).First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != outbox.StatusPublished {
		t.Fatalf("stale markFailed resurrected row: status=%q", got.Status)
	}
}

func TestRelay_ProcessBatch_MaxRetriesMarksFailed(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()

	row := outbox.OutboxEvent{
		Pubsub:  "pubsub",
		Topic:   "user-created",
		Payload: []byte(`{"user_id":"42"}`),
		Status:  outbox.StatusPending,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	oc := mustOutboxCfg(t)
	oc.MaxRetries = 2

	relay := outbox.NewRelay(outbox.NewRelayParams{Client: client, Outbox: oc})
	relay.SetPublisher(&mockPublisher{err: errPublish})

	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	var got outbox.OutboxEvent
	if err := client.Conn(ctx).First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != outbox.StatusPending {
		t.Fatalf("after 1 fail status=%q want pending", got.Status)
	}

	if _, err := relay.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Conn(ctx).First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != outbox.StatusFailed {
		t.Fatalf("after max retries status=%q want failed", got.Status)
	}
	if got.Attempts != 2 {
		t.Fatalf("attempts=%d want 2", got.Attempts)
	}
}

type callbackPublisher struct {
	fn func() error
}

func (c *callbackPublisher) PublishEvent(_ context.Context, _, _ string, _ []byte, _ map[string]string) error {
	return c.fn()
}
