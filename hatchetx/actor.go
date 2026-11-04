package hatchetx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/hatchet-dev/hatchet/pkg/client/types"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/tidwall/gjson"
)

// DefaultActorConcurrencyExpr 以 actor 实例 id 作为 Hatchet 并发键。
// 输入载荷必须把 "actorId" 字段 JSON 序列化出来（见 [ActorRef]）。
const DefaultActorConcurrencyExpr = "input.actorId"

// ActorRef 嵌入到命令载荷中，使并发 Expression "input.actorId" 生效。
// 优先嵌入本类型，而不是只实现裸 GetActorID()——Call/CallAsync 要求
// 序列化后的 JSON 包含 "actorId"。
type ActorRef struct {
	ActorID string `json:"actorId"`
}

// GetActorID 实现 [Actor.Call] 使用的 actor-id 约定。
func (a ActorRef) GetActorID() string { return a.ActorID }

// ActorHandler 是单个 actor 命令的业务处理器。
// Hatchet 并发（MaxRuns=1）保证同一 actorId 内串行。
// 处理器应在外部加载/存储持久状态；worker 进程不具备粘性。
type ActorHandler[In, Out any] func(ctx context.Context, in In) (Out, error)

// Actor 是 Hatchet 上带类型的并发邮箱（语义 actor）：
// 每个 actorId 一个队列，以 MaxRuns=1 和 GROUP_ROUND_ROBIN 处理。
// 它不提供粘性 worker，也不提供内存内 actor 激活。
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

// ActorOption 配置 [NewActor]。
type ActorOption func(*actorOptions)

// WithActorDescription 设置 Hatchet workflow 描述。
func WithActorDescription(desc string) ActorOption {
	return func(o *actorOptions) { o.description = desc }
}

// WithActorConcurrencyExpr 覆盖 CEL 并发键（默认：input.actorId）。
func WithActorConcurrencyExpr(expr string) ActorOption {
	return func(o *actorOptions) { o.concurrencyExpr = expr }
}

// WithActorMaxRuns 覆盖并发 group 的 MaxRuns（默认：1）。
func WithActorMaxRuns(n int32) ActorOption {
	return func(o *actorOptions) {
		if n > 0 {
			o.maxRuns = n
		}
	}
}

// NewActor 定义一个由 Hatchet standalone task 承载的语义 actor 命令。
//
// In 必须把 "actorId" JSON 序列化出来，以配合默认的并发 CEL（嵌入
// [ActorRef]，或使用带 json:"actorId" 的导出字段）。只有 GetActorID() 方法
// 是不够的——Hatchet 基于 JSON 输入而非 Go 方法计算并发。
// 若用 [WithActorConcurrencyExpr] 覆盖 CEL，仍需在 JSON 中保留 actorId，
// 除非自定义表达式改用另一个你能保证存在的输入字段。
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
		// 初始化时提前告警：输入类型带 actor ID 但缺少 json:"actorId"
		if cache != nil && !cache.jsonTagFound && (cache.getter != nil || cache.hasActorID) {
			slog.Warn("hatchetx: NewActor: input type has actor ID but does not JSON-serialize 'actorId' (embed hatchetx.ActorRef or field with json:\"actorId\")",
				"actor", name, "type", inType.String())
		}
	}

	if handler != nil {
		inner := handler
		handler = func(ctx context.Context, in In) (Out, error) {
			start := time.Now()
			out, err := inner(ctx, in)
			recordActorRun(ctx, name, time.Since(start), err != nil)
			return out, err
		}
	}

	return &Actor[In, Out]{name: name, handler: handler, opts: o, typeCache: cache}
}

// Name 返回 Hatchet workflow / task 名称。
func (a *Actor[In, Out]) Name() string {
	if a == nil {
		return ""
	}
	return a.name
}

// Registrar 返回把该 actor 注册到共享 worker 的 [Registrar]。
func (a *Actor[In, Out]) Registrar() Registrar {
	return func(client *Client) ([]WorkflowBase, error) {
		return a.Register(client)
	}
}

// Register 构建带 per-actorId 并发的 Hatchet standalone task。
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
	// 快速路径：若该类型静态含有标记为 json:"actorId" 的 actorId struct 字段，
	// 且未自定义序列化，则 json.Marshal 必然输出 "actorId": id。
	// 此时跳过冗余的预序列化，交由 SDK Run 完成唯一的序列化。
	if a.typeCache != nil && a.typeCache.jsonTagFound && a.typeCache.hasActorID {
		return nil
	}
	return assertActorIDInJSON(in, id)
}

// Call 把 actor 命令入队并等待结果（按 actorId 串行）。
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

// CallAsync 触发 actor 命令且不等待，返回 Hatchet run id。
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

// ProvideActor 把该 actor 的 [Registrar] 发布到 hatchet_registrars fx group。
// 当处理器需要 Fx 依赖时，优先在 registrar 工厂内构造 Actor。
func ProvideActor[In, Out any](actor *Actor[In, Out]) di.Option {
	return ProvideRegistrar(func() Registrar {
		if actor == nil {
			return func(*Client) ([]WorkflowBase, error) { return nil, nil }
		}
		return actor.Registrar()
	})
}

// actorTypeCache 保存 actor 输入类型的预计算反射元数据。
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

	// 若类型实现了 json.Marshaler，自定义序列化可能省略或重命名 actorId。
	// 此时不得绕过 assertActorIDInJSON。
	var jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	if origType.Implements(jsonMarshalerType) || reflect.PointerTo(origType).Implements(jsonMarshalerType) {
		info.jsonTagFound = false
	}

	// 检查 origType 或其指针是否实现 interface{ GetActorID() string }
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

// assertActorIDInJSON 确保 Hatchet CEL input.actorId 看到的 id 与 Call 校验的一致。
// 使用 gjson 从原始 JSON 字节中高性能、零分配地提取。
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
