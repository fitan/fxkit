package gormx

import (
	"context"
	"errors"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAfterCommit_RunsAfterSuccessfulTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewTestClient(db)
	var ran bool
	if err := client.Transaction(context.Background(), func(ctx context.Context) error {
		AfterCommit(ctx, func() { ran = true })
		if ran {
			t.Fatal("hook ran before commit")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("expected AfterCommit hook")
	}
}

func TestAfterCommit_SkippedOnRollback(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewTestClient(db)
	var ran bool
	_ = client.Transaction(context.Background(), func(ctx context.Context) error {
		AfterCommit(ctx, func() { ran = true })
		return errors.New("rollback")
	})
	if ran {
		t.Fatal("hook must not run on rollback")
	}
}

func TestAfterCommit_NoTransactionRunsImmediately(t *testing.T) {
	var ran bool
	AfterCommit(context.Background(), func() { ran = true })
	if !ran {
		t.Fatal("expected immediate run")
	}
}

func TestAfterCommit_NestedSharesOuterHooks(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewTestClient(db)
	var order []string
	if err := client.Transaction(context.Background(), func(ctx context.Context) error {
		AfterCommit(ctx, func() { order = append(order, "outer") })
		return client.Transaction(ctx, func(ctx context.Context) error {
			AfterCommit(ctx, func() { order = append(order, "inner") })
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "outer" || order[1] != "inner" {
		t.Fatalf("order=%v", order)
	}
}

func TestWithTxResult(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewTestClient(db)
	ctx := context.Background()

	// 成功提交并返回泛型值
	val, err := client.WithTxResult(ctx, func(txCtx context.Context) (int, error) {
		return 42, nil
	})
	if err != nil || val != 42 {
		t.Fatalf("expected 42, got %d (err: %v)", val, err)
	}

	// 失败回滚并返回错误与零值
	val, err = client.WithTxResult(ctx, func(txCtx context.Context) (int, error) {
		return 100, errors.New("tx failure")
	})
	if err == nil || val != 0 {
		t.Fatalf("expected error and zero value, got %d %v", val, err)
	}
}
