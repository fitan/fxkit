package crudx_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/gormx"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type demoUser struct {
	ID        int64 `gorm:"primaryKey"`
	Name      string
	Email     string `gorm:"uniqueIndex"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func newDemoRepo(t *testing.T) *crudx.Repo[demoUser] {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:crudx_repo?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := gormx.NewTestClient(db)
	repo, err := crudx.NewRepo[demoUser](crudx.RepoConfig{
		Client:        client,
		UpdateColumns: []string{"name", "email"},
		Spec: crudx.ListSpec{
			Fields: map[string]crudx.FieldSpec{
				"name":  {Column: "demo_users.name", Kind: crudx.FieldString, Indexed: true},
				"email": {Column: "demo_users.email", Kind: crudx.FieldString, Indexed: true},
			},
			SortFields:  map[string]string{"id": "demo_users.id"},
			DefaultSort: "id asc",
			Limits:      crudx.Limits{LimitDefault: 20, LimitMax: 100},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestRepoCRUD(t *testing.T) {
	repo := newDemoRepo(t)
	ctx := context.Background()

	u := &demoUser{Name: "alice", Email: "a@example.com"}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 {
		t.Fatal("expected assigned id")
	}

	got, err := repo.GetByID(ctx, "1")
	if err != nil || got.Email != "a@example.com" {
		t.Fatalf("GetByID: %+v %v", got, err)
	}

	byEmail, err := repo.First(ctx, crudx.WhereInput{
		Query: "email = ?", Args: []any{"a@example.com"}, Detail: "email=a@example.com",
	})
	if err != nil || byEmail.ID != u.ID {
		t.Fatalf("First: %+v %v", byEmail, err)
	}
	ok, err := repo.ExistsWhere(ctx, "email = ?", "a@example.com")
	if err != nil || !ok {
		t.Fatalf("ExistsWhere: %v %v", ok, err)
	}

	// Idempotent update (same values) must not return NotFound.
	if err := repo.Update(ctx, crudx.UpdateInput[demoUser]{Entity: u}); err != nil {
		t.Fatalf("idempotent update: %v", err)
	}

	u.Name = "alice2"
	if err := repo.Update(ctx, crudx.UpdateInput[demoUser]{Entity: u}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetByID(ctx, "1")
	if err != nil || got.Name != "alice2" {
		t.Fatalf("after update: %+v %v", got, err)
	}

	list, err := repo.List(ctx, crudx.ListParams{Limit: 10})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("List: %+v %v", list, err)
	}

	if err := repo.Delete(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByID(ctx, "1"); err == nil {
		t.Fatal("expected not found after delete")
	}
	if err := repo.Delete(ctx, "1"); err == nil {
		t.Fatal("expected not found on second delete")
	}
}

func TestRepoUpdateMissing(t *testing.T) {
	repo := newDemoRepo(t)
	ctx := context.Background()
	err := repo.Update(ctx, crudx.UpdateInput[demoUser]{
		Entity: &demoUser{ID: 999, Name: "x", Email: "x@example.com"},
	})
	if err == nil {
		t.Fatal("expected not found")
	}
}

type demoUserDTO struct {
	ID    int64
	Name  string
	Email string
}

func TestRepo_GenericMethods_ListTo_GetByIDTo(t *testing.T) {
	repo := newDemoRepo(t)
	ctx := context.Background()

	u := &demoUser{Name: "bob", Email: "bob@example.com"}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	// 验证 Repo.GetByIDTo[demoUserDTO]
	dto, err := repo.GetByIDTo(ctx, fmt.Sprintf("%d", u.ID), func(row demoUser) demoUserDTO {
		return demoUserDTO{ID: row.ID, Name: row.Name, Email: row.Email}
	})
	if err != nil {
		t.Fatalf("GetByIDTo failed: %v", err)
	}
	if dto.Name != "bob" || dto.Email != "bob@example.com" {
		t.Fatalf("unexpected dto: %+v", dto)
	}

	// 验证 Repo.ListTo[demoUserDTO]
	res, err := repo.ListTo(ctx, crudx.ListParams{Limit: 10}, func(row demoUser) demoUserDTO {
		return demoUserDTO{ID: row.ID, Name: row.Name, Email: row.Email}
	})
	if err != nil {
		t.Fatalf("ListTo failed: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Name != "bob" {
		t.Fatalf("unexpected list items: %+v", res.Items)
	}
}
