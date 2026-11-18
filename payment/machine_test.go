package payment_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/payment"
)

type paymentRow struct {
	ID      string `gorm:"primaryKey;size:64"`
	State   string `gorm:"size:32;not null"`
	Version int64  `gorm:"not null;default:1"`
}

func (r *paymentRow) PaymentState() payment.State     { return payment.State(r.State) }
func (r *paymentRow) SetPaymentState(s payment.State) { r.State = string(s) }
func (r *paymentRow) GetVersion() int64               { return r.Version }
func (r *paymentRow) SetVersion(v int64)              { r.Version = v }

func TestApplyTransitionAndConflict(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "payment.db")
	client, err := gormx.New(&gormx.Config{Driver: "sqlite", DSN: dsn, LogLevel: "silent"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.Conn(ctx).AutoMigrate(&paymentRow{}); err != nil {
		t.Fatal(err)
	}
	row := paymentRow{ID: "p1", State: "created", Version: 1}
	if err := client.Conn(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	m := payment.NewMachine(
		payment.Transition{From: "created", To: "authorized"},
		payment.Transition{From: "authorized", To: "captured"},
	)

	if err := payment.Apply(ctx, client, m, &row, "authorized"); err != nil {
		t.Fatal(err)
	}
	if row.State != "authorized" || row.Version != 2 {
		t.Fatalf("row=%+v", row)
	}

	// 同一个 stale 副本（version=1）不能再流转。
	stale := paymentRow{ID: "p1", State: "authorized", Version: 1}
	if err := payment.Apply(ctx, client, m, &stale, "captured"); err == nil {
		t.Fatal("expected version conflict")
	}

	// 非法 transition。
	invalid := paymentRow{ID: "p1", State: "created", Version: 2}
	if err := payment.Apply(ctx, client, m, &invalid, "refunded"); err == nil {
		t.Fatal("expected invalid transition")
	}
}
