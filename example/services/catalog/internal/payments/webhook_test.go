package payments_test

import (
	"context"
	"testing"

	"github.com/fitan/fxkit-example/services/catalog/internal/payments"
)

// TestWebhookIsIdempotent 验证同一 eventId 重复投递不会重复记账。
func TestWebhookIsIdempotent(t *testing.T) {
	svc, _, l := setup(t, payments.NewMockProvider())
	ctx := context.Background()

	created, err := svc.Create(ctx, payments.CreatePaymentReq{Amount: 100, Currency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authorize(ctx, created.ID); err != nil {
		t.Fatal(err)
	}

	ev := payments.WebhookEvent{
		EventID:   "evt_1",
		PaymentID: created.ID,
		Type:      "captured",
		Amount:    100,
		Currency:  "CNY",
	}
	got, err := svc.HandleWebhook(ctx, ev)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "captured" {
		t.Fatalf("first webhook status=%s", got.Status)
	}
	// 重复投递：inbox 去重，不应重复记账。
	if _, err := svc.HandleWebhook(ctx, ev); err != nil {
		t.Fatal(err)
	}
	cash, err := l.Balance(ctx, "cash", "CNY")
	if err != nil || cash.Amount != 100 {
		t.Fatalf("cash=%+v err=%v", cash, err)
	}
	entries, err := svc.LedgerEntries(ctx, created.ID)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
}

// TestWebhookAmountMismatch 验证回调金额与支付单不一致时拒绝。
func TestWebhookAmountMismatch(t *testing.T) {
	svc, _, _ := setup(t, payments.NewMockProvider())
	ctx := context.Background()

	created, err := svc.Create(ctx, payments.CreatePaymentReq{Amount: 100, Currency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authorize(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.HandleWebhook(ctx, payments.WebhookEvent{
		EventID:   "evt_bad",
		PaymentID: created.ID,
		Type:      "captured",
		Amount:    999,
		Currency:  "CNY",
	})
	if err == nil {
		t.Fatal("expected amount mismatch")
	}
}
