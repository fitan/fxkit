package hatchetx

import (
	"context"
	"encoding/json"
	"reflect"
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

type structFieldNoTagInput struct {
	ActorID string
	Delta   int `json:"delta"`
}

type namedRefInput struct {
	Ref   ActorRef `json:"ref"`
	Delta int      `json:"delta"`
}

type customMarshalInput struct {
	ActorRef
	Data string `json:"data"`
}

func (c customMarshalInput) MarshalJSON() ([]byte, error) {
	return []byte(`{"data":"custom"}`), nil
}

type unexportedInnerRef struct {
	ActorRef
}

type unexportedEmbedInput struct {
	unexportedInnerRef
	Data string `json:"data"`
}

type ptrReceiverInput struct {
	ID string `json:"actorId"`
}

func (p *ptrReceiverInput) GetActorID() string {
	if p == nil {
		return ""
	}
	return p.ID
}

type nilPtrEmbedInput struct {
	*ActorRef
	Data string `json:"data"`
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
		{"unexported_embed", unexportedEmbedInput{unexportedInnerRef: unexportedInnerRef{ActorRef: ActorRef{ActorID: "u1"}}, Data: "d"}, "u1"},
		{"nil_ptr_embed", nilPtrEmbedInput{ActorRef: nil, Data: "d"}, ""},
		{"ptr_receiver_val", ptrReceiverInput{ID: "ptr1"}, "ptr1"},
		{"ptr_receiver_ptr", &ptrReceiverInput{ID: "ptr2"}, "ptr2"},
		{"empty", embedInput{}, ""},
		{"nil", nil, ""},
		{"raw_json", json.RawMessage(`{"actorId":"raw-1","x":1}`), "raw-1"},
		{"bytes_json", []byte(`{"actorId":"bytes-1"}`), "bytes-1"},
		{"map_any", map[string]any{"actorId": "map-any-1"}, "map-any-1"},
		{"map_string", map[string]string{"actorId": "map-str-1"}, "map-str-1"},
	}
	for _, tc := range cases {
		tt := tc
		t.Run(tt.name, func(t *testing.T) {
			got := extractActorID(tt.in)
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestExtractActorIDDynamic_UnexportedEmbed(t *testing.T) {
	t.Parallel()
	in := unexportedEmbedInput{unexportedInnerRef: unexportedInnerRef{ActorRef: ActorRef{ActorID: "dyn-1"}}, Data: "d"}
	got := extractActorIDDynamic(reflect.ValueOf(in))
	if got != "dyn-1" {
		t.Fatalf("expected dyn-1, got %q", got)
	}
}

func TestActorTypeCache(t *testing.T) {
	t.Parallel()

	// Verify embedInput cache
	c1 := getActorTypeCache(reflect.TypeFor[embedInput]())
	if !c1.hasActorID || !c1.jsonTagFound || len(c1.fieldIndex) == 0 {
		t.Fatalf("expected embedInput to have cached actorId field, got: %+v", c1)
	}

	// Verify methodOnlyInput cache
	c2 := getActorTypeCache(reflect.TypeFor[methodOnlyInput]())
	if c2.getter == nil {
		t.Fatalf("expected methodOnlyInput to have getter, got: %+v", c2)
	}
	if c2.jsonTagFound {
		t.Fatalf("expected methodOnlyInput to NOT have jsonTagFound, got: %+v", c2)
	}

	// Verify pointer type cache
	c3 := getActorTypeCache(reflect.TypeFor[*fieldInput]())
	if !c3.isPtr || !c3.hasActorID || len(c3.fieldIndex) == 0 {
		t.Fatalf("expected *fieldInput to be ptr with fieldIndex, got: %+v", c3)
	}

	// Verify structFieldNoTagInput cache: hasActorID is true, but jsonTagFound MUST BE FALSE!
	c4 := getActorTypeCache(reflect.TypeFor[structFieldNoTagInput]())
	if !c4.hasActorID {
		t.Fatalf("expected structFieldNoTagInput to have hasActorID=true, got: %+v", c4)
	}
	if c4.jsonTagFound {
		t.Fatalf("expected structFieldNoTagInput to have jsonTagFound=false, got: %+v", c4)
	}

	// Verify namedRefInput cache: non-anonymous field must NOT be treated as root actorId
	c5 := getActorTypeCache(reflect.TypeFor[namedRefInput]())
	if c5.hasActorID || c5.jsonTagFound {
		t.Fatalf("expected namedRefInput to NOT have hasActorID or jsonTagFound, got: %+v", c5)
	}

	// Verify customMarshalInput cache: custom json.Marshaler must NOT have jsonTagFound=true
	c6 := getActorTypeCache(reflect.TypeFor[customMarshalInput]())
	if c6.jsonTagFound {
		t.Fatalf("expected customMarshalInput to have jsonTagFound=false due to MarshalJSON, got: %+v", c6)
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
	id, err = requireActorID(unexportedEmbedInput{unexportedInnerRef: unexportedInnerRef{ActorRef: ActorRef{ActorID: "u1"}}})
	if err != nil || id != "u1" {
		t.Fatalf("unexported id=%q err=%v", id, err)
	}
}

func TestRequireActorID_RejectsStructFieldWithoutJSON(t *testing.T) {
	t.Parallel()
	_, err := requireActorID(structFieldNoTagInput{ActorID: "s1"})
	if err == nil {
		t.Fatal("expected error for ActorID field without json actorId")
	}
	if !strings.Contains(err.Error(), "JSON-serialize") {
		t.Fatalf("unexpected err: %v", err)
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

func TestAssertActorIDInJSON_RawMessage(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"actorId":"raw-1","x":1}`)
	if err := assertActorIDInJSON(raw, "raw-1"); err != nil {
		t.Fatal(err)
	}
}

func TestNewActor_DefaultConcurrencyMailbox(t *testing.T) {
	t.Parallel()
	a := NewActor[embedInput, int](" mailbox ",
		func(ctx context.Context, in embedInput) (int, error) { return in.Delta, nil },
	)
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
	TenantID string `json:"tenantId"`
	Data     string `json:"data"`
}

func TestNewActor_CustomConcurrencyExpr(t *testing.T) {
	t.Parallel()
	a := NewActor[tenantInput, int]("tenant-queue",
		func(ctx context.Context, in tenantInput) (int, error) { return len(in.Data), nil },
		WithActorConcurrencyExpr("input.tenantId"),
	)
	if a.requiresActorID() {
		t.Fatal("custom concurrency expr input.tenantId should not require actorId")
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

func TestActor_Call_EmptyName(t *testing.T) {
	t.Parallel()
	a := NewActor[embedInput, int]("",
		func(ctx context.Context, in embedInput) (int, error) { return 0, nil },
	)
	if _, err := a.Call(context.Background(), &Client{}, embedInput{}); err == nil || !strings.Contains(err.Error(), "actor name is required") {
		t.Fatalf("expected actor name required error, got: %v", err)
	}
	if _, err := a.CallAsync(context.Background(), &Client{}, embedInput{}); err == nil || !strings.Contains(err.Error(), "actor name is required") {
		t.Fatalf("expected actor name required error, got: %v", err)
	}
}

func TestActor_Call_RequiresActorID(t *testing.T) {
	t.Parallel()
	a := NewActor[embedInput, int]("x",
		func(ctx context.Context, in embedInput) (int, error) { return 0, nil },
	)
	// Disabled client fails first.
	if _, err := a.Call(context.Background(), &Client{}, embedInput{}); err == nil {
		t.Fatal("expected error")
	}
}

func BenchmarkExtractActorID_Cached(b *testing.B) {
	a := NewActor[embedInput, int]("bench", func(ctx context.Context, in embedInput) (int, error) { return in.Delta, nil })
	in := embedInput{ActorRef: ActorRef{ActorID: "bench-actor-1"}, Delta: 42}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := a.extractActorID(in)
		if id == "" {
			b.Fatal("empty id")
		}
	}
}

func BenchmarkAssertActorIDInJSON_GJSON(b *testing.B) {
	in := embedInput{ActorRef: ActorRef{ActorID: "bench-actor-1"}, Delta: 42}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := assertActorIDInJSON(in, "bench-actor-1"); err != nil {
			b.Fatal(err)
		}
	}
}
