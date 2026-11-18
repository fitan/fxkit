package hatchetx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type embedInput struct {
	ActorRef
	Delta int `json:"delta"`
}

type indirectInner struct {
	ActorRef
}

type indirectEmbedInput struct {
	indirectInner
	Delta int `json:"delta"`
}

type namedRefEmbedInput struct {
	ActorRef `json:"ref"`
	Delta    int `json:"delta"`
}

type ptrRefEmbedInput struct {
	*ActorRef
	Delta int `json:"delta"`
}

type customMarshalInput struct {
	ActorRef
	Data string `json:"data"`
}

func (c customMarshalInput) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"data": c.Data})
}

type stringInput string

func (s stringInput) ActorRefValue() ActorRef { return ActorRef{} }

func TestNewActor_RequiresDirectActorRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{
			name: "indirect embed",
			run: func() error {
				_, err := NewActor[indirectEmbedInput, int]("x", func(context.Context, indirectEmbedInput) (int, error) { return 0, nil })
				return err
			},
			want: "anonymous value field",
		},
		{
			name: "named embed",
			run: func() error {
				_, err := NewActor[namedRefEmbedInput, int]("x", func(context.Context, namedRefEmbedInput) (int, error) { return 0, nil })
				return err
			},
			want: "must not have a json name",
		},
		{
			name: "pointer embed",
			run: func() error {
				_, err := NewActor[ptrRefEmbedInput, int]("x", func(context.Context, ptrRefEmbedInput) (int, error) { return 0, nil })
				return err
			},
			want: "anonymous value field",
		},
		{
			name: "custom marshaler drops actorId",
			run: func() error {
				_, err := NewActor[customMarshalInput, int]("x", func(context.Context, customMarshalInput) (int, error) { return 0, nil })
				return err
			},
			want: "top-level",
		},
		{
			name: "non-struct",
			run: func() error {
				_, err := NewActor[stringInput, int]("x", func(context.Context, stringInput) (int, error) { return 0, nil })
				return err
			},
			want: "must be a struct",
		},
		{
			name: "nil handler",
			run: func() error {
				_, err := NewActor[embedInput, int]("x", nil)
				return err
			},
			want: "handler is nil",
		},
		{
			name: "empty name",
			run: func() error {
				_, err := NewActor[embedInput, int]("  ", func(context.Context, embedInput) (int, error) { return 0, nil })
				return err
			},
			want: "name is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.run()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestNewActor_DefaultConcurrencyMailbox(t *testing.T) {
	t.Parallel()
	a, err := NewActor[embedInput, int](" mailbox ",
		func(ctx context.Context, in embedInput) (int, error) { return in.Delta, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "mailbox" {
		t.Fatalf("name should be trimmed, got %q", a.Name())
	}
	if a.opts.concurrencyExpr != DefaultActorConcurrencyExpr {
		t.Fatalf("expr=%q want %q", a.opts.concurrencyExpr, DefaultActorConcurrencyExpr)
	}
	if a.opts.maxRuns != 1 {
		t.Fatalf("maxRuns=%d want 1 (same actorId must serialize)", a.opts.maxRuns)
	}
}

type tenantInput struct {
	ActorRef
	TenantID string `json:"tenantId"`
	Data     string `json:"data"`
}

func TestNewActor_CustomConcurrencyExpr(t *testing.T) {
	t.Parallel()
	a, err := NewActor[tenantInput, int]("tenant-queue",
		func(ctx context.Context, in tenantInput) (int, error) { return len(in.Data), nil },
		WithActorConcurrencyExpr("input.tenantId"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if a.requiresActorID() {
		t.Fatal("custom concurrency expr input.tenantId should not require actorId")
	}
}

func TestNewActor_Options(t *testing.T) {
	t.Parallel()
	a, err := NewActor[embedInput, int]("fxkit-counter",
		func(ctx context.Context, in embedInput) (int, error) { return in.Delta, nil },
		WithActorDescription("demo"),
		WithActorConcurrencyExpr("input.customId"),
		WithActorMaxRuns(2),
	)
	if err != nil {
		t.Fatal(err)
	}
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

func TestActor_Register_DisabledClient(t *testing.T) {
	t.Parallel()
	a, err := NewActor[embedInput, int]("x",
		func(ctx context.Context, in embedInput) (int, error) { return 0, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := a.Register(&Client{})
	if err != nil {
		t.Fatal(err)
	}
	if ws != nil {
		t.Fatalf("expected nil workflows, got %d", len(ws))
	}
}

func TestActor_ValidateActorID(t *testing.T) {
	t.Parallel()
	a, err := NewActor[embedInput, int]("x",
		func(ctx context.Context, in embedInput) (int, error) { return 0, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.validateActorID(embedInput{}); err == nil {
		t.Fatal("expected actorId error")
	}
	if err := a.validateActorID(embedInput{ActorRef: ActorRef{ActorID: "a1"}}); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkExtractActorID_Cached(b *testing.B) {
	a, err := NewActor[embedInput, int]("bench", func(ctx context.Context, in embedInput) (int, error) { return in.Delta, nil })
	if err != nil {
		b.Fatal(err)
	}
	in := embedInput{ActorRef: ActorRef{ActorID: "bench-actor-1"}, Delta: 42}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := a.extractActorID(in)
		if id == "" {
			b.Fatal("empty id")
		}
	}
}
