package hatchetx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"

	"github.com/hatchet-dev/hatchet/pkg/client/types"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/tidwall/gjson"
	"github.com/fitan/fxkit/di"
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
	name      string
	handler   ActorHandler[In, Out]
	opts      actorOptions
	typeCache *actorTypeCache
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
	name = strings.TrimSpace(name)
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

	inType := reflect.TypeFor[In]()
	cache := getActorTypeCache(inType)

	if o.concurrencyExpr == DefaultActorConcurrencyExpr {
		// Warn early at initialization time if input type has actor ID but lacks json:"actorId"
		if cache != nil && !cache.jsonTagFound && (cache.getter != nil || cache.hasActorID) {
			slog.Warn("hatchetx: NewActor: input type has actor ID but does not JSON-serialize 'actorId' (embed hatchetx.ActorRef or field with json:\"actorId\")",
				"actor", name, "type", inType.String())
		}
	}

	return &Actor[In, Out]{name: name, handler: handler, opts: o, typeCache: cache}
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

func (a *Actor[In, Out]) requiresActorID() bool {
	if a == nil {
		return true
	}
	expr := strings.TrimSpace(a.opts.concurrencyExpr)
	return expr == "" || expr == DefaultActorConcurrencyExpr || strings.Contains(expr, "actorId")
}

func (a *Actor[In, Out]) extractActorID(in In) string {
	switch v := any(in).(type) {
	case json.RawMessage:
		return strings.TrimSpace(gjson.GetBytes(v, "actorId").String())
	case []byte:
		return strings.TrimSpace(gjson.GetBytes(v, "actorId").String())
	case map[string]any:
		if id, ok := v["actorId"].(string); ok {
			return strings.TrimSpace(id)
		}
	case map[string]string:
		if id, ok := v["actorId"]; ok {
			return strings.TrimSpace(id)
		}
	}
	if a != nil && a.typeCache != nil {
		if id := extractActorIDFromCache(in, a.typeCache); id != "" {
			return id
		}
	}
	return extractActorID(in)
}

func (a *Actor[In, Out]) validateActorID(in In) error {
	id := a.extractActorID(in)
	if id == "" {
		return fmt.Errorf("hatchetx: actorId is required (embed hatchetx.ActorRef or field with json:\\\"actorId\\\")")
	}
	// Fast path: if the type statically has an actorId struct field tagged json:"actorId",
	// and does not customize marshaling, json.Marshal is guaranteed to output "actorId": id.
	// In this case, we avoid a redundant pre-marshal and let SDK Run perform the sole serialization.
	if a.typeCache != nil && a.typeCache.jsonTagFound && a.typeCache.hasActorID {
		return nil
	}
	return assertActorIDInJSON(in, id)
}

// Call enqueues the actor command and waits for the result (serialized by actorId).
func (a *Actor[In, Out]) Call(ctx context.Context, client *Client, in In, opts ...hatchet.RunOptFunc) (Out, error) {
	var zero Out
	if a == nil {
		return zero, fmt.Errorf("hatchetx: actor is nil")
	}
	name := strings.TrimSpace(a.name)
	if name == "" {
		return zero, fmt.Errorf("hatchetx: actor name is required")
	}
	if client == nil || !client.Enabled() {
		return zero, fmt.Errorf("hatchetx: client disabled")
	}
	if a.requiresActorID() {
		if err := a.validateActorID(in); err != nil {
			return zero, err
		}
	} else {
		if _, err := json.Marshal(in); err != nil {
			return zero, fmt.Errorf("hatchetx: marshal actor input: %w", err)
		}
	}
	result, err := client.SDK.Run(ctx, name, in, opts...)
	if err != nil {
		return zero, fmt.Errorf("hatchetx: actor %q call: %w", name, err)
	}
	if result == nil {
		return zero, fmt.Errorf("hatchetx: actor %q returned nil result", name)
	}
	if err := result.TaskOutput(name).Into(&zero); err != nil {
		return zero, fmt.Errorf("hatchetx: actor %q result: %w", name, err)
	}
	return zero, nil
}

// CallAsync fires the actor command without waiting. Returns the Hatchet run id.
func (a *Actor[In, Out]) CallAsync(ctx context.Context, client *Client, in In, opts ...hatchet.RunOptFunc) (string, error) {
	if a == nil {
		return "", fmt.Errorf("hatchetx: actor is nil")
	}
	name := strings.TrimSpace(a.name)
	if name == "" {
		return "", fmt.Errorf("hatchetx: actor name is required")
	}
	if client == nil || !client.Enabled() {
		return "", fmt.Errorf("hatchetx: client disabled")
	}
	if a.requiresActorID() {
		if err := a.validateActorID(in); err != nil {
			return "", err
		}
	} else {
		if _, err := json.Marshal(in); err != nil {
			return "", fmt.Errorf("hatchetx: marshal actor input: %w", err)
		}
	}
	return client.RunNoWait(ctx, name, in, opts...)
}

// ProvideActor publishes the actor's [Registrar] into the hatchet_registrars fx group.
// Prefer constructing the Actor inside a registrar factory when the handler needs Fx deps.
func ProvideActor[In, Out any](actor *Actor[In, Out]) di.Option {
	return ProvideRegistrar(func() Registrar {
		if actor == nil {
			return func(*Client) ([]WorkflowBase, error) { return nil, nil }
		}
		return actor.Registrar()
	})
}

// actorTypeCache holds precomputed reflection metadata for an actor input type.
type actorTypeCache struct {
	hasActorID   bool
	fieldIndex   []int
	getter       func(any) string
	isPtr        bool
	jsonTagFound bool
}

var (
	actorTypeCacheMu  sync.RWMutex
	actorTypeCacheMap = make(map[reflect.Type]*actorTypeCache)
)

func getActorTypeCache(t reflect.Type) *actorTypeCache {
	if t == nil {
		return nil
	}
	actorTypeCacheMu.RLock()
	cached, ok := actorTypeCacheMap[t]
	actorTypeCacheMu.RUnlock()
	if ok {
		return cached
	}

	actorTypeCacheMu.Lock()
	defer actorTypeCacheMu.Unlock()
	if cached, ok := actorTypeCacheMap[t]; ok {
		return cached
	}

	cached = buildActorTypeCache(t)
	actorTypeCacheMap[t] = cached
	return cached
}

func hasNilEmbeddedPtr(v reflect.Value) bool {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return false
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			vf := v.Field(i)
			if f.Type.Kind() == reflect.Pointer {
				if vf.IsNil() {
					return true
				}
				if hasNilEmbeddedPtr(vf.Elem()) {
					return true
				}
			} else if f.Type.Kind() == reflect.Struct {
				if hasNilEmbeddedPtr(vf) {
					return true
				}
			}
		}
	}
	return false
}

func buildActorTypeCache(origType reflect.Type) *actorTypeCache {
	info := &actorTypeCache{}
	t := origType

	for t.Kind() == reflect.Pointer {
		info.isPtr = true
		t = t.Elem()
	}

	if t.Kind() == reflect.Struct {
		indexPath, jsonFound := findActorIDField(t, nil)
		if len(indexPath) > 0 {
			info.fieldIndex = indexPath
			info.hasActorID = true
			info.jsonTagFound = jsonFound
		}
	}

	// If the type implements json.Marshaler, custom serialization may omit or rename actorId.
	// In that case, we must not bypass assertActorIDInJSON.
	var jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	if origType.Implements(jsonMarshalerType) || reflect.PointerTo(origType).Implements(jsonMarshalerType) {
		info.jsonTagFound = false
	}

	// Check if origType or pointer implements interface{ GetActorID() string }
	checkType := origType
	if checkType.Kind() != reflect.Pointer {
		checkType = reflect.PointerTo(checkType)
	}
	if checkType.Implements(reflect.TypeFor[interface{ GetActorID() string }]()) {
		info.getter = func(in any) (id string) {
			if in == nil {
				return ""
			}
			defer func() {
				if r := recover(); r != nil {
					id = ""
				}
			}()
			v := reflect.ValueOf(in)
			if hasNilEmbeddedPtr(v) {
				return ""
			}
			if h, ok := in.(interface{ GetActorID() string }); ok {
				return strings.TrimSpace(h.GetActorID())
			}
			if v.CanInterface() {
				if v.Kind() != reflect.Pointer {
					pv := reflect.New(v.Type())
					pv.Elem().Set(v)
					if h, ok := pv.Interface().(interface{ GetActorID() string }); ok {
						return strings.TrimSpace(h.GetActorID())
					}
				}
			}
			return ""
		}
	}

	return info
}

func findActorIDField(t reflect.Type, currentPath []int) ([]int, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		path := append(append([]int(nil), currentPath...), i)

		jsonTag := f.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		isJSONActorID := jsonTag == "actorId" || strings.HasPrefix(jsonTag, "actorId,")
		isNameActorID := f.Name == "ActorID"

		if (isJSONActorID || isNameActorID) && f.Type.Kind() == reflect.String {
			return path, isJSONActorID
		}

		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if subPath, subFound := findActorIDField(ft, path); len(subPath) > 0 {
					return subPath, subFound
				}
			}
		}
	}
	return nil, false
}

func safeFieldByIndex(v reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct || i >= v.NumField() {
			return reflect.Value{}
		}
		v = v.Field(i)
	}
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func extractActorIDFromCache(in any, cache *actorTypeCache) (id string) {
	if in == nil || cache == nil {
		return ""
	}
	defer func() {
		if r := recover(); r != nil {
			id = ""
		}
	}()
	if len(cache.fieldIndex) > 0 {
		v := reflect.ValueOf(in)
		for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return ""
			}
			v = v.Elem()
		}
		f := safeFieldByIndex(v, cache.fieldIndex)
		if f.IsValid() && f.Kind() == reflect.String {
			if id := strings.TrimSpace(f.String()); id != "" {
				return id
			}
		}
	}
	if cache.getter != nil {
		if id := cache.getter(in); id != "" {
			return id
		}
	}
	return ""
}

func requireActorID(in any) (string, error) {
	id := extractActorID(in)
	if id == "" {
		return "", fmt.Errorf("hatchetx: actorId is required (embed hatchetx.ActorRef or field with json:\\\"actorId\\\")")
	}
	if err := assertActorIDInJSON(in, id); err != nil {
		return "", err
	}
	return id, nil
}

// assertActorIDInJSON ensures Hatchet CEL input.actorId sees the same id Call validated.
// Uses gjson for high-performance zero-allocation extraction from raw JSON bytes.
func assertActorIDInJSON(in any, want string) error {
	var raw []byte
	switch v := in.(type) {
	case []byte:
		raw = v
	case json.RawMessage:
		raw = v
	default:
		var err error
		raw, err = json.Marshal(in)
		if err != nil {
			return fmt.Errorf("hatchetx: marshal actor input: %w", err)
		}
	}

	res := gjson.GetBytes(raw, "actorId")
	if !res.Exists() {
		return fmt.Errorf("hatchetx: input must JSON-serialize \\\"actorId\\\" for concurrency (embed hatchetx.ActorRef or json:\\\"actorId\\\"); GetActorID alone is not enough")
	}
	got := strings.TrimSpace(res.String())
	if got == "" {
		return fmt.Errorf("hatchetx: input must JSON-serialize \\\"actorId\\\" for concurrency (embed hatchetx.ActorRef or json:\\\"actorId\\\"); GetActorID alone is not enough")
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
	switch v := in.(type) {
	case json.RawMessage:
		return strings.TrimSpace(gjson.GetBytes(v, "actorId").String())
	case []byte:
		return strings.TrimSpace(gjson.GetBytes(v, "actorId").String())
	case map[string]any:
		if id, ok := v["actorId"].(string); ok {
			return strings.TrimSpace(id)
		}
	case map[string]string:
		if id, ok := v["actorId"]; ok {
			return strings.TrimSpace(id)
		}
	}
	cache := getActorTypeCache(reflect.TypeOf(in))
	if id := extractActorIDFromCache(in, cache); id != "" {
		return id
	}
	v := reflect.ValueOf(in)
	return extractActorIDDynamic(v)
}

func extractActorIDDynamic(v reflect.Value) (id string) {
	defer func() {
		if r := recover(); r != nil {
			id = ""
		}
	}()
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	if !hasNilEmbeddedPtr(v) && v.CanInterface() {
		if v.CanAddr() {
			if h, ok := v.Addr().Interface().(interface{ GetActorID() string }); ok {
				if id := strings.TrimSpace(h.GetActorID()); id != "" {
					return id
				}
			}
		} else {
			pv := reflect.New(v.Type())
			pv.Elem().Set(v)
			if h, ok := pv.Interface().(interface{ GetActorID() string }); ok {
				if id := strings.TrimSpace(h.GetActorID()); id != "" {
					return id
				}
			}
		}
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		vf := v.Field(i)
		if f.Anonymous {
			if id := extractActorIDDynamic(vf); id != "" {
				return id
			}
		}
		if f.Tag.Get("json") == "-" {
			continue
		}
		isJSONActorID := f.Tag.Get("json") == "actorId" || strings.HasPrefix(f.Tag.Get("json"), "actorId,")
		if f.Name == "ActorID" || isJSONActorID {
			for vf.Kind() == reflect.Pointer || vf.Kind() == reflect.Interface {
				if vf.IsNil() {
					break
				}
				vf = vf.Elem()
			}
			if vf.IsValid() && vf.Kind() == reflect.String {
				return strings.TrimSpace(vf.String())
			}
		}
	}
	return ""
}
