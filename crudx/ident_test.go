package crudx

import (
	"context"
	"testing"
)

func TestList_RejectsInvalidSpecIdentifiers(t *testing.T) {
	db := newListTestDB(t)
	ctx := context.Background()

	spec := listTestSpec()
	spec.Fields["bad"] = FieldSpec{Column: "name; DROP TABLE x"}
	if _, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   spec,
		Params: ListParams{Limit: 10},
		ToRow:  func(m listTestModel) listTestRow { return listTestRow{ID: m.ID} },
	}); err == nil {
		t.Fatal("invalid field column must be rejected")
	}

	spec = listTestSpec()
	spec.SortFields["bad"] = "name OR 1=1"
	if _, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   spec,
		Params: ListParams{Limit: 10},
		ToRow:  func(m listTestModel) listTestRow { return listTestRow{ID: m.ID} },
	}); err == nil {
		t.Fatal("invalid sort column must be rejected")
	}
}

func TestList_RejectsInvalidSelect(t *testing.T) {
	db := newListTestDB(t)
	ctx := context.Background()
	if _, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   listTestSpec(),
		Params: ListParams{Limit: 10},
		Select: []string{"name; DROP TABLE x"},
		ToRow:  func(m listTestModel) listTestRow { return listTestRow{ID: m.ID} },
	}); err == nil {
		t.Fatal("invalid select column must be rejected")
	}
}

func TestList_AllowsTableStarAndQualifiedColumns(t *testing.T) {
	db := newListTestDB(t)
	ctx := context.Background()
	if _, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   listTestSpec(),
		Params: ListParams{Limit: 10},
		Select: []string{"list_test_models.*"},
		ToRow:  func(m listTestModel) listTestRow { return listTestRow{ID: m.ID} },
	}); err != nil {
		t.Fatalf("table.* should be allowed: %v", err)
	}
}
