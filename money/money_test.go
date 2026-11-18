package money_test

import (
	"testing"

	"github.com/fitan/fxkit/money"
)

func TestMoney_Arithmetic(t *testing.T) {
	a := money.MustNew(100, "cny")
	b := money.MustNew(50, "CNY")

	sum, err := a.Add(b)
	if err != nil || sum.Amount != 150 || sum.Currency != "CNY" {
		t.Fatalf("sum=%+v err=%v", sum, err)
	}
	diff, err := a.Sub(b)
	if err != nil || diff.Amount != 50 {
		t.Fatalf("diff=%+v err=%v", diff, err)
	}
	if a.Neg().Amount != -100 {
		t.Fatalf("neg=%d", a.Neg().Amount)
	}
	if a.Mul(3).Amount != 300 {
		t.Fatalf("mul=%d", a.Mul(3).Amount)
	}
	if cmp, err := a.Compare(b); err != nil || cmp != 1 {
		t.Fatalf("compare=%d err=%v", cmp, err)
	}
	if a.IsZero() {
		t.Fatal("100 should not be zero")
	}
	if got := a.String(); got != "100 CNY" {
		t.Fatalf("string=%q", got)
	}
}

func TestMoney_CurrencyMismatch(t *testing.T) {
	a := money.MustNew(100, "CNY")
	b := money.MustNew(100, "USD")
	if _, err := a.Add(b); err == nil {
		t.Fatal("expected currency mismatch")
	}
	if _, err := a.Compare(b); err == nil {
		t.Fatal("expected currency mismatch")
	}
	if _, err := money.New(1, "cny1"); err == nil {
		t.Fatal("expected invalid currency")
	}
}
