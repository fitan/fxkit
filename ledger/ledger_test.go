package ledger_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/ledger"
)

func testLedger(t *testing.T) (*ledger.Ledger, context.Context) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "ledger.db")
	client, err := gormx.New(&gormx.Config{Driver: "sqlite", DSN: dsn, LogLevel: "silent"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l := ledger.New(client)
	if err := l.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return l, ctx
}

func TestLedger_PostBalanceAndEntries(t *testing.T) {
	l, ctx := testLedger(t)

	err := l.Post(ctx, ledger.Transaction{ID: "tx1", Reference: "payment-1"}, []ledger.Posting{
		{AccountID: "cash", Amount: 100, Currency: "cny"},
		{AccountID: "revenue", Amount: -100, Currency: "CNY"},
	})
	if err != nil {
		t.Fatal(err)
	}

	bal, err := l.Balance(ctx, "cash", "CNY")
	if err != nil || bal.Amount != 100 {
		t.Fatalf("balance=%+v err=%v", bal, err)
	}
	entries, err := l.Entries(ctx, "tx1")
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}

	// 不平衡必须拒绝。
	if err := l.Post(ctx, ledger.Transaction{ID: "tx2"}, []ledger.Posting{
		{AccountID: "a", Amount: 1, Currency: "CNY"},
		{AccountID: "b", Amount: -2, Currency: "CNY"},
	}); err == nil {
		t.Fatal("expected unbalanced postings to fail")
	}

	// 重复 txn 必须拒绝。
	if err := l.Post(ctx, ledger.Transaction{ID: "tx1"}, []ledger.Posting{
		{AccountID: "cash", Amount: 1, Currency: "CNY"},
		{AccountID: "revenue", Amount: -1, Currency: "CNY"},
	}); err == nil {
		t.Fatal("expected duplicate transaction to fail")
	}

	// 金额为 0 必须拒绝。
	if err := l.Post(ctx, ledger.Transaction{ID: "tx3"}, []ledger.Posting{
		{AccountID: "a", Amount: 0, Currency: "CNY"},
		{AccountID: "b", Amount: 0, Currency: "CNY"},
	}); err == nil {
		t.Fatal("expected zero amount to fail")
	}
}
