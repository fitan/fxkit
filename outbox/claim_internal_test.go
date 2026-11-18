package outbox

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestClaimBatch_ClaimsPendingAndExpiredOnly 是回归测试：
//
// claimSkipLocked 与 claimSQLite 曾各写一份 WHERE 条件 + 排序 + attempts+1，
// 改一处就会让两种方言的语义漂移。这里直接验证认领条件与回填：
// pending 与「租约过期」的 processing 可被认领，未过期的 processing 不可。
func TestClaimBatch_ClaimsPendingAndExpiredOnly(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:claim_internal?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	staleAt := time.Now().UTC().Add(-time.Hour)
	freshAt := time.Now().UTC()
	rows := []OutboxEvent{
		{Pubsub: "p", Topic: "t", Payload: []byte(`{"a":1}`), Status: StatusPending},
		{Pubsub: "p", Topic: "t", Payload: []byte(`{"b":2}`), Status: StatusProcessing, LockedAt: &staleAt, LeaseID: "old"},
		{Pubsub: "p", Topic: "t", Payload: []byte(`{"c":3}`), Status: StatusProcessing, LockedAt: &freshAt, LeaseID: "new"},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := claimBatch(ctx, db, claimParams{
		Limit:       10,
		StaleBefore: time.Now().UTC().Add(-30 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed=%d, want 2 (pending + expired; fresh processing must be skipped)", len(claimed))
	}
	for _, ev := range claimed {
		if ev.Attempts != 0 || ev.LeaseID == "" || ev.Status != StatusProcessing {
			t.Fatalf("claimed row not marked (attempts must stay 0): %+v", ev)
		}
	}
}
