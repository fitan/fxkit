package outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/fitan/fxkit/outbox"
)

func TestStore_PurgePublished(t *testing.T) {
	client := testDB(t)
	store := outbox.NewStore(client)
	ctx := context.Background()

	oldT := time.Now().UTC().Add(-48 * time.Hour)
	newT := time.Now().UTC()
	rows := []outbox.OutboxEvent{
		{Pubsub: "pubsub", Topic: "old", Payload: []byte(`{}`), Status: outbox.StatusPublished, PublishedAt: &oldT},
		{Pubsub: "pubsub", Topic: "new", Payload: []byte(`{}`), Status: outbox.StatusPublished, PublishedAt: &newT},
		{Pubsub: "pubsub", Topic: "pending", Payload: []byte(`{}`), Status: outbox.StatusPending},
		{Pubsub: "pubsub", Topic: "failed", Payload: []byte(`{}`), Status: outbox.StatusFailed},
	}
	for i := range rows {
		if err := client.Conn(ctx).Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	n, err := store.PurgePublished(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged=%d, want 1", n)
	}

	var count int64
	if err := client.Conn(ctx).Model(&outbox.OutboxEvent{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("remaining=%d, want 3 (new published + pending + failed)", count)
	}

	if _, err := store.PurgePublished(ctx, time.Time{}); err == nil {
		t.Fatal("zero cutoff must be rejected")
	}
}
