package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fitan/fxkit/outbox"
)

func newInbox(t *testing.T) (*outbox.Inbox, context.Context) {
	t.Helper()
	client := testDB(t)
	ctx := context.Background()
	if err := client.Conn(ctx).AutoMigrate(&outbox.InboxEvent{}); err != nil {
		t.Fatal(err)
	}
	return outbox.NewInboxWithTimeout(client, time.Minute), ctx
}

// TestInbox_RetriesAfterHandlerFailure 回归：handler 失败后释放认领，下一次可以重试。
func TestInbox_RetriesAfterHandlerFailure(t *testing.T) {
	inbox, ctx := newInbox(t)
	calls := 0
	fn := func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("transient")
		}
		return nil
	}
	if err := inbox.Once(ctx, outbox.OnceInput{Key: "k", Topic: "t", Fn: fn}); err == nil {
		t.Fatal("first call should fail")
	}
	if err := inbox.Once(ctx, outbox.OnceInput{Key: "k", Topic: "t", Fn: fn}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2", calls)
	}
}

// TestInbox_ReclaimsStaleProcessing 回归：处理中的行在认领超时后可被重新认领。
func TestInbox_ReclaimsStaleProcessing(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()
	if err := client.Conn(ctx).AutoMigrate(&outbox.InboxEvent{}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Hour)
	row := outbox.InboxEvent{
		ID:          "stale",
		Topic:       "t",
		Status:      outbox.InboxProcessing,
		ClaimedAt:   &old,
		LeaseID:     "dead-lease",
		ProcessedAt: old,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	var stored outbox.InboxEvent
	if err := client.Conn(ctx).Where("id = ?", "stale").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	t.Logf("stored: status=%q claimed=%v lease=%q", stored.Status, stored.ClaimedAt, stored.LeaseID)

	inbox := outbox.NewInboxWithTimeout(client, time.Minute)
	calls := 0
	if err := inbox.Once(ctx, outbox.OnceInput{Key: "stale", Topic: "t", Fn: func(context.Context) error {
		calls++
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("stale claim was not reclaimed (calls=%d)", calls)
	}

	var got outbox.InboxEvent
	if err := client.Conn(ctx).Where("id = ?", "stale").First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != outbox.InboxDone {
		t.Fatalf("status=%q, want done", got.Status)
	}
}

// TestInbox_InProgressIsNotSuccess 回归：未过期的 processing 不能当成已完成。
func TestInbox_InProgressIsNotSuccess(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()
	if err := client.Conn(ctx).AutoMigrate(&outbox.InboxEvent{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	row := outbox.InboxEvent{
		ID:          "busy",
		Topic:       "t",
		Status:      outbox.InboxProcessing,
		ClaimedAt:   &now,
		LeaseID:     "other",
		ProcessedAt: now,
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	inbox := outbox.NewInboxWithTimeout(client, time.Minute)
	calls := 0
	err := inbox.Once(ctx, outbox.OnceInput{Key: "busy", Topic: "t", Fn: func(context.Context) error {
		calls++
		return nil
	}})
	if !errors.Is(err, outbox.ErrInboxBusy) {
		t.Fatalf("err=%v, want ErrInboxBusy", err)
	}
	if calls != 0 {
		t.Fatalf("fn calls=%d, in-progress key must not run", calls)
	}
}
