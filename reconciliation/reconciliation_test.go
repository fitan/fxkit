package reconciliation_test

import (
	"context"
	"testing"

	"github.com/fitan/fxkit/reconciliation"
)

type row struct {
	ID     string
	Amount int64
}

func TestRunFindsMissingAndMismatch(t *testing.T) {
	internal := []row{{ID: "a", Amount: 100}, {ID: "b", Amount: 200}, {ID: "c", Amount: 300}}
	external := []row{{ID: "a", Amount: 100}, {ID: "b", Amount: 999}, {ID: "d", Amount: 400}}

	diffs, err := reconciliation.Run(context.Background(), reconciliation.Comparator[row]{
		Internal: func(context.Context) ([]row, error) { return internal, nil },
		External: func(context.Context) ([]row, error) { return external, nil },
		Key:      func(r row) string { return r.ID },
		Equal:    func(a, b row) bool { return a.Amount == b.Amount },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 3 {
		t.Fatalf("diffs=%+v", diffs)
	}
	if diffs[0].Key != "b" || diffs[0].Kind != reconciliation.Mismatch {
		t.Fatalf("diffs[0]=%+v", diffs[0])
	}
	if diffs[1].Key != "c" || diffs[1].Kind != reconciliation.MissingExternal {
		t.Fatalf("diffs[1]=%+v", diffs[1])
	}
	if diffs[2].Key != "d" || diffs[2].Kind != reconciliation.MissingInternal {
		t.Fatalf("diffs[2]=%+v", diffs[2])
	}
	summary := reconciliation.Summary(diffs)
	if summary[reconciliation.Mismatch] != 1 || summary[reconciliation.MissingExternal] != 1 || summary[reconciliation.MissingInternal] != 1 {
		t.Fatalf("summary=%v", summary)
	}
}
