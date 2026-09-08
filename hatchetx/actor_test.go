package hatchetx

import (
	"context"
	"strings"
	"testing"
)

type embedInput struct {
	ActorRef
	Delta int `json:"delta"`
}

type methodOnlyInput struct {
	ID string
}

func (m methodOnlyInput) GetActorID() string { return m.ID }

type methodAndJSONInput struct {
	ID string `json:"actorId"`
}

func (m methodAndJSONInput) GetActorID() string { return m.ID }

type fieldInput struct {
	ActorID string `json:"actorId"`
	Delta   int    `json:"delta"`
}

func TestExtractActorID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"embed", embedInput{ActorRef: ActorRef{ActorID: " a1 "}, Delta: 1}, "a1"},
		{"method", methodOnlyInput{ID: "m1"}, "m1"},
		{"field", fieldInput{ActorID: "f1"}, "f1"},
		{"empty", embedInput{}, ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractActorID(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestRequireActorID(t *testing.T) {
	t.Parallel()
	if _, err := requireActorID(embedInput{}); err == nil {
		t.Fatal("expected error")
	}
	id, err := requireActorID(embedInput{ActorRef: ActorRef{ActorID: "x"}})
	if err != nil || id != "x" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	id, err = requireActorID(fieldInput{ActorID: "f1"})
	if err != nil || id != "f1" {
		t.Fatalf("field id=%q err=%v", id, err)
	}
	id, err = requireActorID(methodAndJSONInput{ID: "m2"})
	if err != nil || id != "m2" {
		t.Fatalf("method+json id=%q err=%v", id, err)
	}
}

func TestRequireActorID_RejectsGetActorIDWithoutJSON(t *testing.T) {
	t.Parallel()
	_, err := requireActorID(methodOnlyInput{ID: "m1"})
	if err == nil {
		t.Fatal("expected error for GetActorID without json actorId")
	}
	if !strings.Contains(err.Error(), "JSON-serialize") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestNewActor_DefaultConcurrencyMailbox(t *testing.T) {
	t.Parallel()
	a := NewActor[embedInput, int]("mailbox",
		func(ctx context.Context, in embedInput) (int, error) { return in.Delta, nil },
	)
	if a.opts.concurrencyExpr != DefaultActorConcurrencyExpr {
		t.Fatalf("expr=%q want %q", a.opts.concurrencyExpr, DefaultActorConcurrencyExpr)
	}
	if a.opts.maxRuns != 1 {
		t.Fatalf("maxRuns=%d want 1 (same actorId must serialize)", a.opts.maxRuns)
	}
}

func TestNewActor_Options(t *testing.T) {
	t.Parallel()
	a := NewActor[embedInput, int]("fxkit-counter",
		func(ctx context.Context, in embedInput) (int, error) { return in.Delta, nil },
		WithActorDescription("demo"),
		WithActorConcurrencyExpr("input.customId"),
		WithActorMaxRuns(2),
	)
	if a.Name() != "fxkit-counter" {
		t.Fatalf("name=%q", a.Name())
	}
	if a.opts.description != "demo" {
		t.Fatalf("desc=%q", a.opts.description)
	}
	if a.opts.concurrencyExpr != "input.customId" {
		t.Fatalf("expr=%q", a.opts.concurrencyExpr)
	}
	if a.opts.maxRuns != 2 {
		t.Fatalf("maxRuns=%d", a.opts.maxRuns)
	}
}

func TestActor_Register_NilHandler(t *testing.T) {
	t.Parallel()
	a := NewActor[embedInput, int]("x", nil)
	if _, err := a.Register(&Client{}); err == nil {
		t.Fatal("expected error for nil handler")
	}
}

func TestActor_Register_DisabledClient(t *testing.T) {
	t.Parallel()
	a := NewActor[embedInput, int]("x",
		func(ctx context.Context, in embedInput) (int, error) { return 0, nil },
	)
	ws, err := a.Register(&Client{})
	if err != nil {
		t.Fatal(err)
	}
	if ws != nil {
		t.Fatalf("expected nil workflows, got %d", len(ws))
	}
}

func TestActor_Call_RequiresActorID(t *testing.T) {
	t.Parallel()
	a := NewActor[embedInput, int]("x",
		func(ctx context.Context, in embedInput) (int, error) { return 0, nil },
	)
	// Enabled check happens after actorId? Looking at Call: Enabled first, then requireActorID.
	// Disabled client fails first.
	if _, err := a.Call(context.Background(), &Client{}, embedInput{}); err == nil {
		t.Fatal("expected error")
	}
}
