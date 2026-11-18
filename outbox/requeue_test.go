package outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/fitan/fxkit/outbox"
)

func TestStore_RequeueFailed(t *testing.T) {
	client := testDB(t)
	store := outbox.NewStore(client)
	ctx := context.Background()

	future := time.Now().Add(time.Hour)
	rows := []outbox.OutboxEvent{
		{Pubsub: "p", Topic: "a", Payload: []byte(`{}`), Status: outbox.StatusFailed, Attempts: 10, LastError: "boom", NextAttemptAt: &future},
		{Pubsub: "p", Topic: "b", Payload: []byte(`{}`), Status: outbox.StatusFailed, Attempts: 10, LastError: "boom"},
		{Pubsub: "p", Topic: "a", Payload: []byte(`{}`), Status: outbox.StatusPending},
	}
	for i := range rows {
		if err := client.Conn(ctx).Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	n, err := store.RequeueFailed(ctx, outbox.RequeueFilter{Topics: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("requeued=%d, want 1", n)
	}

	var got outbox.OutboxEvent
	if err := client.Conn(ctx).First(&got, rows[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != outbox.StatusPending || got.Attempts != 0 || got.NextAttemptAt != nil || got.LastError != "" {
		t.Fatalf("row not reset: %+v", got)
	}

	var b outbox.OutboxEvent
	if err := client.Conn(ctx).First(&b, rows[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if b.Status != outbox.StatusFailed {
		t.Fatalf("topic b should stay failed, got %q", b.Status)
	}
}
