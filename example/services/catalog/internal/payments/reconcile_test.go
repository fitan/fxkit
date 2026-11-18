package payments_test

import (
	"context"
	"testing"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
	"github.com/fitan/fxkit-example/services/catalog/internal/payments"
)

// mismatchProvider 故意让渠道侧金额 +1，用于验证对账能发现 mismatch。
type mismatchProvider struct {
	settled map[string]payments.SettlementRow
}

// Authorize 返回固定的假渠道引用。
func (p *mismatchProvider) Authorize(context.Context, entity.Payment, string) (string, error) {
	return "fake", nil
}

// Capture 故意把结算金额 +1，制造对账差异。
func (p *mismatchProvider) Capture(_ context.Context, pmt entity.Payment, _ string) error {
	if p.settled == nil {
		p.settled = map[string]payments.SettlementRow{}
	}
	p.settled[pmt.ID] = payments.SettlementRow{PaymentID: pmt.ID, Amount: pmt.Amount + 1, Currency: pmt.Currency}
	return nil
}

// Refund 不改变假结算数据。
func (p *mismatchProvider) Refund(context.Context, entity.Payment, int64, string) error { return nil }

// Settlements 返回渠道侧结算行。
func (p *mismatchProvider) Settlements(context.Context) ([]payments.SettlementRow, error) {
	out := make([]payments.SettlementRow, 0, len(p.settled))
	for _, row := range p.settled {
		out = append(out, row)
	}
	return out, nil
}

// TestReconcileFindsMismatch 断言内部记账与渠道结算不一致时能定位到具体支付单。
func TestReconcileFindsMismatch(t *testing.T) {
	svc, _, _ := setup(t, &mismatchProvider{})
	ctx := context.Background()

	created, err := svc.Create(ctx, payments.CreatePaymentReq{Amount: 100, Currency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authorize(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Capture(ctx, created.ID); err != nil {
		t.Fatal(err)
	}

	diffs, err := svc.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 || diffs[0].Kind != "mismatch" || diffs[0].Key != created.ID {
		t.Fatalf("diffs=%+v", diffs)
	}
}
