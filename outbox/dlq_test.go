package outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/fitan/fxkit/outbox"
)

func TestStore_PurgeFailedAndListFailed(t *testing.T) {
	client := testDB(t)
	store := outbox.NewStore(client)
	ctx := context.Background()

	oldT := time.Now().UTC().Add(-48 * time.Hour)
	newT := time.Now().UTC()
	rows := []outbox.OutboxEvent{
		{Pubsub: "p", Topic: "failed-old", Payload: []byte(`{}`), Status: outbox.StatusFailed, CreatedAt: oldT},
		{Pubsub: "p", Topic: "failed-new", Payload: []byte(`{}`), Status: outbox.StatusFailed, CreatedAt: newT},
		{Pubsub: "p", Topic: "published-old", Payload: []byte(`{}`), Status: outbox.StatusPublished, PublishedAt: &oldT, CreatedAt: oldT},
		{Pubsub: "p", Topic: "pending-old", Payload: []byte(`{}`), Status: outbox.StatusPending, CreatedAt: oldT},
	}
	for i := range rows {
		if err := client.Conn(ctx).Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	failed, err := store.ListFailed(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 2 {
		t.Fatalf("failed=%d, want 2", len(failed))
	}

	n, err := store.PurgeFailed(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged failed=%d, want 1", n)
	}
}

func TestInbox_PurgeDoneAndListStale(t *testing.T) {
	client := testDB(t)
	inbox := outbox.NewInbox(client)
	ctx := context.Background()
	if err := inbox.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	oldT := time.Now().UTC().Add(-48 * time.Hour)
	now := time.Now().UTC()
	rows := []outbox.InboxEvent{
		{ID: "done-old", Topic: "t", Status: outbox.InboxDone, ProcessedAt: oldT},
		{ID: "done-new", Topic: "t", Status: outbox.InboxDone, ProcessedAt: now},
		{ID: "stale", Topic: "t", Status: outbox.InboxProcessing, ClaimedAt: &oldT, ProcessedAt: oldT},
		{ID: "busy", Topic: "t", Status: outbox.InboxProcessing, ClaimedAt: &now, ProcessedAt: now},
	}
	for i := range rows {
		if err := client.Conn(ctx).Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	stale, err := inbox.ListStale(ctx, time.Now().UTC().Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].ID != "stale" {
		t.Fatalf("stale=%+v", stale)
	}

	n, err := inbox.PurgeDone(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged done=%d, want 1", n)
	}
}
