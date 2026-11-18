package payments_test

import (
	"context"
	"testing"

	"github.com/fitan/fxkit-example/services/catalog/internal/payments"
)

// TestPartialRefunds 覆盖多次部分退款、超额拒绝、退完后状态与账本均衡。
func TestPartialRefunds(t *testing.T) {
	svc, _, l := setup(t, payments.NewMockProvider())
	ctx := context.Background()

	created, err := svc.Create(ctx, payments.CreatePaymentReq{Amount: 100, Currency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authorize(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	captured, err := svc.Capture(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if captured.CapturedAmount != 100 {
		t.Fatalf("capturedAmount=%d, want 100", captured.CapturedAmount)
	}

	// 第一次部分退款 30：状态仍为 captured，累计退款 30。
	after, err := svc.Refund(ctx, created.ID, payments.RefundReq{Amount: 30})
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "captured" || after.RefundedAmount != 30 {
		t.Fatalf("after partial refund status=%s refunded=%d", after.Status, after.RefundedAmount)
	}
	cash, err := l.Balance(ctx, "cash", "CNY")
	if err != nil || cash.Amount != 70 {
		t.Fatalf("cash=%+v err=%v", cash, err)
	}
	entries, err := svc.LedgerEntries(ctx, created.ID)
	if err != nil || len(entries) != 4 {
		t.Fatalf("entries=%d err=%v, want 4", len(entries), err)
	}

	// 超额退款必须拒绝。
	if _, err := svc.Refund(ctx, created.ID, payments.RefundReq{Amount: 71}); err == nil {
		t.Fatal("expected over-refund to fail")
	}

	// 省略金额 = 退剩余 70：状态转 refunded，账本归零。
	after, err = svc.Refund(ctx, created.ID, payments.RefundReq{})
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "refunded" || after.RefundedAmount != 100 {
		t.Fatalf("after full refund status=%s refunded=%d", after.Status, after.RefundedAmount)
	}
	cash, err = l.Balance(ctx, "cash", "CNY")
	if err != nil || cash.Amount != 0 {
		t.Fatalf("cash=%+v err=%v", cash, err)
	}
	entries, err = svc.LedgerEntries(ctx, created.ID)
	if err != nil || len(entries) != 6 {
		t.Fatalf("entries=%d err=%v, want 6", len(entries), err)
	}

	// 已退完，再次退款必须拒绝。
	if _, err := svc.Refund(ctx, created.ID, payments.RefundReq{Amount: 1}); err == nil {
		t.Fatal("expected refund after full refund to fail")
	}
}
