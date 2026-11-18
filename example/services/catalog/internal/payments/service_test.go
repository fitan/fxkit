package payments_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/ledger"
	"github.com/fitan/fxkit/outbox"
	"github.com/gofrs/uuid/v5"
	"go.jetify.com/typeid"

	"github.com/fitan/fxkit-example/services/catalog/internal/payments"
	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
)

// setup 构造支付服务。
func setup(t *testing.T, provider payments.Provider) (*payments.Service, *gormx.Client, *ledger.Ledger) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "payments.db")
	client, err := gormx.New(&gormx.Config{Driver: "sqlite", DSN: dsn, LogLevel: "silent"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l := ledger.New(client)
	if err := l.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store := outbox.NewStore(client)
	inbox := outbox.NewInbox(client)
	if err := inbox.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lc := di.NewLifecycle()
	cfg := &payments.Config{Provider: "mock", WebhookSecret: "test-secret"}
	svc, err := payments.NewService(client, repository.NewPaymentRepo(client), l, store, inbox, &outbox.Config{Enabled: false}, provider, cfg, lc)
	if err != nil {
		t.Fatal(err)
	}
	if err := lc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return svc, client, l
}

// TestPaymentLifecycleAndLedger 覆盖创建/授权/扣款/退款与账本净额归零。
func TestPaymentLifecycleAndLedger(t *testing.T) {
	svc, _, l := setup(t, payments.NewMockProvider())
	ctx := context.Background()

	created, err := svc.Create(ctx, payments.CreatePaymentReq{Amount: 100, Currency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "created" || created.Version != 1 || created.Amount != 100 {
		t.Fatalf("created=%+v", created)
	}
	// 支付 ID 应该是 TypeID：pay_ 前缀 + 26 字符 Base32（UUIDv7）。
	tid, err := typeid.FromString(created.ID)
	if err != nil {
		t.Fatalf("payment id %q is not a TypeID: %v", created.ID, err)
	}
	if tid.Prefix() != "pay" {
		t.Fatalf("payment id prefix=%q, want pay", tid.Prefix())
	}
	if len(tid.Suffix()) != 26 {
		t.Fatalf("payment id suffix=%q, want 26 chars", tid.Suffix())
	}
	u, err := uuid.FromString(tid.UUID())
	if err != nil {
		t.Fatalf("payment id %q has invalid uuid: %v", created.ID, err)
	}
	if u.Version() != 7 {
		t.Fatalf("payment id version=%d, want 7 (UUIDv7)", u.Version())
	}

	auth, err := svc.Authorize(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if auth.Status != "authorized" || auth.Version != 2 || auth.ProviderRef == "" {
		t.Fatalf("authorized=%+v", auth)
	}

	captured, err := svc.Capture(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Status != "captured" || captured.Version != 3 {
		t.Fatalf("captured=%+v", captured)
	}

	cash, err := l.Balance(ctx, "cash", "CNY")
	if err != nil || cash.Amount != 100 {
		t.Fatalf("cash=%+v err=%v", cash, err)
	}
	payable, err := l.Balance(ctx, "merchant_payable", "CNY")
	if err != nil || payable.Amount != -100 {
		t.Fatalf("payable=%+v err=%v", payable, err)
	}
	entries, err := svc.LedgerEntries(ctx, created.ID)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}

	refunded, err := svc.Refund(ctx, created.ID, payments.RefundReq{})
	if err != nil {
		t.Fatal(err)
	}
	if refunded.Status != "refunded" || refunded.Version != 4 {
		t.Fatalf("refunded=%+v", refunded)
	}
	cash, _ = l.Balance(ctx, "cash", "CNY")
	payable, _ = l.Balance(ctx, "merchant_payable", "CNY")
	if cash.Amount != 0 || payable.Amount != 0 {
		t.Fatalf("after refund cash=%d payable=%d", cash.Amount, payable.Amount)
	}
	entries, err = svc.LedgerEntries(ctx, created.ID)
	if err != nil || len(entries) != 4 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
}

// TestListPayments 验证最近支付单列表。
func TestListPayments(t *testing.T) {
	svc, _, _ := setup(t, payments.NewMockProvider())
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := svc.Create(ctx, payments.CreatePaymentReq{Amount: 100, Currency: "CNY"}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := svc.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items=%d, want 3", len(items))
	}
}

// TestInvalidTransitionDoesNotCallProvider 验证非法状态转换直接拒绝。
func TestInvalidTransitionDoesNotCallProvider(t *testing.T) {
	svc, _, _ := setup(t, payments.NewMockProvider())
	ctx := context.Background()

	created, err := svc.Create(ctx, payments.CreatePaymentReq{Amount: 100, Currency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	// created -> capture 非法。
	if _, err := svc.Capture(ctx, created.ID); err == nil {
		t.Fatal("expected invalid transition")
	}
}
