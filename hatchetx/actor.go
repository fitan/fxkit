package hatchetx

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hatchet-dev/hatchet/pkg/client/types"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"go.uber.org/fx"
)

// DefaultActorConcurrencyExpr keys Hatchet concurrency on the actor instance id.
// Input payloads must JSON-serialize field "actorId" (see [ActorRef]).
const DefaultActorConcurrencyExpr = "input.actorId"

// ActorRef embeds into command payloads so concurrency Expression "input.actorId" works.
// Prefer embedding this over a bare GetActorID() — Call/CallAsync require the
// marshaled JSON to contain "actorId".
type ActorRef struct {
	ActorID string `json:"actorId"`
}

// GetActorID implements the actor-id contract used by [Actor.Call].
func (a ActorRef) GetActorID() string { return a.ActorID }

// ActorHandler is the business handler for one actor command.
// Serialization is guaranteed per actorId by Hatchet concurrency (MaxRuns=1).
// Handlers should load/store durable state externally; the worker process is not sticky.
type ActorHandler[In, Out any] func(ctx context.Context, in In) (Out, error)

// Actor is a typed concurrency mailbox on Hatchet (semantic actor):
// one queue per actorId, processed with MaxRuns=1 and GROUP_ROUND_ROBIN.
// It does not provide sticky workers or in-memory actor activation.
type Actor[In, Out any] struct {
	name    string
	handler ActorHandler[In, Out]
	opts    actorOptions
}

type actorOptions struct {
	description     string
	concurrencyExpr string
	maxRuns         int32
}

// ActorOption configures [NewActor].
type ActorOption func(*actorOptions)

// WithActorDescription sets the Hatchet workflow description.
func WithActorDescription(desc string) ActorOption {
	return func(o *actorOptions) { o.description = desc }
}

// WithActorConcurrencyExpr overrides the CEL concurrency key (default: input.actorId).
func WithActorConcurrencyExpr(expr string) ActorOption {
	return func(o *actorOptions) { o.concurrencyExpr = expr }
}

// WithActorMaxRuns overrides MaxRuns for the concurrency group (default: 1).
func WithActorMaxRuns(n int32) ActorOption {
	return func(o *actorOptions) {
		if n > 0 {
			o.maxRuns = n
		}
	}
}

// NewActor defines a semantic actor command backed by a Hatchet standalone task.
//
// In must JSON-serialize "actorId" for the default concurrency CEL (embed
// [ActorRef] or an exported field with json:"actorId"). A GetActorID() method
// alone is not enough — Hatchet evaluates concurrency on the JSON input, not Go methods.
// If you override the CEL with [WithActorConcurrencyExpr], still keep actorId in
// JSON unless the custom expression keys on another input field you guarantee.
func NewActor[In, Out any](name string, handler ActorHandler[In, Out], opts ...ActorOption) *Actor[In, Out] {
	o := actorOptions{
		concurrencyExpr: DefaultActorConcurrencyExpr,
		maxRuns:         1,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.description == "" {
		o.description = fmt.Sprintf("semantic actor %q (concurrency mailbox by actorId)", name)
	}
	return &Actor[In, Out]{name: name, handler: handler, opts: o}
}

// Name returns the Hatchet workflow / task name.
func (a *Actor[In, Out]) Name() string {
	if a == nil {
		return ""
	}
	return a.name
}

// Registrar returns a [Registrar] that registers this actor on the shared worker.
func (a *Actor[In, Out]) Registrar() Registrar {
	return func(client *Client) ([]WorkflowBase, error) {
		return a.Register(client)
	}
}

// Register builds the Hatchet standalone task with per-actorId concurrency.
func (a *Actor[In, Out]) Register(client *Client) ([]WorkflowBase, error) {
	if a == nil || a.handler == nil {
		return nil, fmt.Errorf("hatchetx: actor is nil")
	}
	if client == nil || !client.Enabled() {
		return nil, nil
	}
	name := strings.TrimSpace(a.name)
	if name == "" {
		return nil, fmt.Errorf("hatchetx: actor name is required")
	}

	maxRuns := a.opts.maxRuns
	strategy := types.GroupRoundRobin
	handler := a.handler
	task := client.SDK.NewStandaloneTask(
		name,
		func(ctx hatchet.Context, in In) (Out, error) {
			return handler(ctx, in)
		},
		hatchet.WithWorkflowDescription(a.opts.description),
		hatchet.WithWorkflowConcurrency(types.Concurrency{
			Expression:    a.opts.concurrencyExpr,
			MaxRuns:       &maxRuns,
			LimitStrategy: &strategy,
		}),
	)
	return []WorkflowBase{task}, nil
}

// Call enqueues the actor command and waits for the result (serialized by actorId).
func (a *Actor[In, Out]) Call(ctx context.Context, client *Client, in In) (Out, error) {
	var zero Out
	if a == nil {
		return zero, fmt.Errorf("hatchetx: actor is nil")
	}
	if client == nil || !client.Enabled() {
		return zero, fmt.Errorf("hatchetx: client disabled")
	}
	if _, err := requireActorID(in); err != nil {
		return zero, err
	}
	result, err := client.SDK.Run(ctx, a.name, in)
	if err != nil {
		return zero, fmt.Errorf("hatchetx: actor %q call: %w", a.name, err)
	}
	if err := result.TaskOutput(a.name).Into(&zero); err != nil {
		return zero, fmt.Errorf("hatchetx: actor %q result: %w", a.name, err)
	}
	return zero, nil
}

// CallAsync fires the actor command without waiting. Returns the Hatchet run id.
func (a *Actor[In, Out]) CallAsync(ctx context.Context, client *Client, in In) (string, error) {
	if a == nil {
		return "", fmt.Errorf("hatchetx: actor is nil")
	}
	if client == nil || !client.Enabled() {
		return "", fmt.Errorf("hatchetx: client disabled")
	}
	if _, err := requireActorID(in); err != nil {
		return "", err
	}
	return client.RunNoWait(ctx, a.name, in)
}

// ProvideActor publishes the actor's [Registrar] into the hatchet_registrars fx group.
// Prefer constructing the Actor inside a registrar factory when the handler needs Fx deps.
func ProvideActor[In, Out any](actor *Actor[In, Out]) fx.Option {
	return ProvideRegistrar(func() Registrar {
		if actor == nil {
			return func(*Client) ([]WorkflowBase, error) { return nil, nil }
		}
		return actor.Registrar()
	})
}

func requireActorID(in any) (string, error) {
	id := extractActorID(in)
	if id == "" {
		return "", fmt.Errorf("hatchetx: actorId is required (embed hatchetx.ActorRef or field with json:\"actorId\")")
	}
	if err := assertActorIDInJSON(in, id); err != nil {
		return "", err
	}
	return id, nil
}

// assertActorIDInJSON ensures Hatchet CEL input.actorId sees the same id Call validated.
// GetActorID()-only types fail here when they omit json:"actorId".
func assertActorIDInJSON(in any, want string) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("hatchetx: marshal actor input: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("hatchetx: decode actor input: %w", err)
	}
	got, _ := m["actorId"].(string)
	got = strings.TrimSpace(got)
	if got == "" {
		return fmt.Errorf("hatchetx: input must JSON-serialize \"actorId\" for concurrency (embed hatchetx.ActorRef or json:\"actorId\"); GetActorID alone is not enough")
	}
	if got != want {
		return fmt.Errorf("hatchetx: json actorId %q disagrees with GetActorID/ActorID %q", got, want)
	}
	return nil
}

func extractActorID(in any) string {
	if in == nil {
		return ""
	}
	if h, ok := in.(interface{ GetActorID() string }); ok {
		return strings.TrimSpace(h.GetActorID())
	}
	v := reflect.ValueOf(in)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			if id := extractActorID(v.Field(i).Interface()); id != "" {
				return id
			}
		}
		if f.Name == "ActorID" || f.Tag.Get("json") == "actorId" || strings.HasPrefix(f.Tag.Get("json"), "actorId,") {
			if v.Field(i).Kind() == reflect.String {
				return strings.TrimSpace(v.Field(i).String())
			}
		}
	}
	return ""
}
