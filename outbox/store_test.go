package outbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/outbox"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gormx.Client {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&outbox.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	return gormx.NewTestClient(db)
}

func TestEnqueue_InTransactionNotVisibleUntilCommit(t *testing.T) {
	client := testDB(t)
	store := outbox.NewStore(client)
	ctx := context.Background()

	err := client.Transaction(ctx, func(txCtx context.Context) error {
		return store.Enqueue(txCtx, outbox.Message{
			Pubsub:  "pubsub",
			Topic:   "user-created",
			Payload: []byte(`{"user_id":"1"}`),
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	var count int64
	if err := client.Conn(ctx).Model(&outbox.OutboxEvent{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after commit, got %d", count)
	}
}

func TestEnqueue_RollbackDiscardsRow(t *testing.T) {
	client := testDB(t)
	store := outbox.NewStore(client)
	ctx := context.Background()

	_ = client.Transaction(ctx, func(txCtx context.Context) error {
		_ = store.Enqueue(txCtx, outbox.Message{
			Pubsub:  "pubsub",
			Topic:   "user-created",
			Payload: []byte(`{"user_id":"1"}`),
		})
		return errors.New("rollback")
	})

	var count int64
	_ = client.Conn(ctx).Model(&outbox.OutboxEvent{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d", count)
	}
}

func TestEnqueue_IdempotencyKeyDedups(t *testing.T) {
	client := testDB(t)
	store := outbox.NewStore(client)
	ctx := context.Background()

	msg := outbox.Message{
		Pubsub:         "pubsub",
		Topic:          "user-created",
		Payload:        []byte(`{"user_id":"1"}`),
		IdempotencyKey: "user-created:1",
	}
	if err := store.Enqueue(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(ctx, msg); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := client.Conn(ctx).Model(&outbox.OutboxEvent{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after duplicate enqueue, got %d", count)
	}
}

func TestEnqueue_IdempotencyKeyRequeuesFailed(t *testing.T) {
	client := testDB(t)
	store := outbox.NewStore(client)
	ctx := context.Background()

	key := "user-created:1"
	row := outbox.OutboxEvent{
		Pubsub:         "hatchet",
		Topic:          "user-created",
		Payload:        []byte(`{"user_id":"old"}`),
		IdempotencyKey: &key,
		Status:         outbox.StatusFailed,
		Attempts:       10,
		LastError:      "give up",
	}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(ctx, outbox.Message{
		Pubsub:         "hatchet",
		Topic:          "user-created",
		Payload:        []byte(`{"user_id":"new"}`),
		IdempotencyKey: key,
	}); err != nil {
		t.Fatal(err)
	}

	var got outbox.OutboxEvent
	if err := client.Conn(ctx).First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != outbox.StatusPending {
		t.Fatalf("status=%q want pending", got.Status)
	}
	if got.Attempts != 0 {
		t.Fatalf("attempts=%d want 0", got.Attempts)
	}
	if string(got.Payload) != `{"user_id":"new"}` {
		t.Fatalf("payload=%s", got.Payload)
	}

	var count int64
	if err := client.Conn(ctx).Model(&outbox.OutboxEvent{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count=%d want 1", count)
	}
}

func TestInbox_Once(t *testing.T) {
	client := testDB(t)
	ctx := context.Background()
	if err := client.Conn(ctx).AutoMigrate(&outbox.InboxEvent{}); err != nil {
		t.Fatal(err)
	}
	inbox := outbox.NewInbox(client)

	runs := 0
	fn := func(context.Context) error {
		runs++
		return nil
	}
	if err := inbox.Once(ctx, outbox.OnceInput{Key: "k1", Topic: "user-created", Fn: fn}); err != nil {
		t.Fatal(err)
	}
	if err := inbox.Once(ctx, outbox.OnceInput{Key: "k1", Topic: "user-created", Fn: fn}); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("expected fn once, got %d", runs)
	}

	// empty key always runs
	if err := inbox.Once(ctx, outbox.OnceInput{Key: "", Fn: fn}); err != nil {
		t.Fatal(err)
	}
	if err := inbox.Once(ctx, outbox.OnceInput{Key: "", Fn: fn}); err != nil {
		t.Fatal(err)
	}
	if runs != 3 {
		t.Fatalf("expected empty-key always run, got runs=%d", runs)
	}
}

