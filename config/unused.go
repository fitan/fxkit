package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/knadh/koanf/v2"
)

// consumerRegistry 记录「哪些配置段被真正读取过」及其类型，供 [Config.Unused] 比对。
//
// 一个段可能被多个包用不同结构体消费（例如 discovery 同时被 consulx 与 reqx 读取），
// 因此 value 是 []reflect.Type 而不是单个类型——否则后注册者会覆盖前者，
// 导致 `app validate` 校验错结构体、`Unused()` 误报死键。
var (
	consumerRegistryMu sync.RWMutex
	consumerRegistry   = map[string][]reflect.Type{}
)

// RegisterConsumer 声明某个配置段由类型 T 消费。
//
// [Load] 与 [Provide] 会自动调用它；手写 UnmarshalKey 的场景可显式登记，
// 否则该段下的键会被 [Config.Unused] 判为「未使用」。
func RegisterConsumer[T any](key string) {
	t := reflect.TypeFor[T]()
	norm := normalizeSection(key)
	consumerRegistryMu.Lock()
	defer consumerRegistryMu.Unlock()
	for _, existing := range consumerRegistry[norm] {
		if existing == t {
			return
		}
	}
	consumerRegistry[norm] = append(consumerRegistry[norm], t)
}

// RunRegisteredValidators 重新解析所有已登记的配置段，触发其 SetDefaults/Validate。
//
// 供 `app validate` 使用：只做结构体级校验（纯内存），不触碰任何外部依赖，
// 因此在没有数据库/缓存的 CI 环境也能跑。
func RunRegisteredValidators(i di.Injector) error {
	if i == nil {
		return fmt.Errorf("config: nil injector")
	}
	cfg, err := di.Invoke[*Config](i)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	consumerRegistryMu.RLock()
	snapshot := make(map[string][]reflect.Type, len(consumerRegistry))
	for key, types := range consumerRegistry {
		snapshot[key] = append([]reflect.Type(nil), types...)
	}
	consumerRegistryMu.RUnlock()

	var firstErr error
	for key, types := range snapshot {
		for _, typ := range types {
			if typ == nil {
				continue
			}
			if err := validateSection(cfg, key, typ); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				break
			}
		}
	}
	return firstErr
}

// validateSection 按登记的动态类型解析配置段并执行其 Validate()。
func validateSection(cfg *Config, key string, typ reflect.Type) error {
	ptr := reflect.New(typ)
	target := ptr.Interface()

	if d, ok := target.(interface{ SetDefaults() }); ok {
		d.SetDefaults()
	}
	if err := cfg.UnmarshalKey(key, target); err != nil {
		return fmt.Errorf("config load %q: %w", key, err)
	}
	if v, ok := target.(interface{ Validate() error }); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("config %s: %w", key, err)
		}
	}
	return nil
}

// consumerTypes 返回某个配置段已登记的结构体类型。
func consumerTypes(key string) ([]reflect.Type, bool) {
	consumerRegistryMu.RLock()
	defer consumerRegistryMu.RUnlock()
	types := consumerRegistry[normalizeSection(key)]
	if len(types) == 0 {
		return nil, false
	}
	out := make([]reflect.Type, len(types))
	copy(out, types)
	return out, true
}

// unusedKey 是一条「写进了配置但未被任何结构体读取」的记录。
type unusedKey struct {
	Key    string
	Reason string
}

// Unused 返回配置树中疑似未被使用的键。
//
// 判定方式：
//   - 对每个顶级段，若该段有登记的消费类型，则逐字段比对 yaml tag；
//     配置里存在但结构体没有对应 yaml tag 的键 → 报告。
//   - 若整个顶级段没有任何登记（没有模块读过它）→ 整段报告。
//
// 注意：这是启发式检查，用于 CI/启动自检告警，不影响正常运行。
// 某些段可能由宿主自行 UnmarshalKey 读取（未登记），此时会误报，
// 用 [RegisterConsumer] 登记即可消除。
func (c *Config) Unused() []UnusedKey {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	k := c.k
	c.mu.RUnlock()
	if k == nil {
		return nil
	}

	var out []UnusedKey
	for _, section := range topSections(k) {
		types, ok := consumerTypes(section)
		if !ok {
			out = append(out, UnusedKey{
				Key:    section,
				Reason: "no module consumes this config section",
			})
			continue
		}
		out = append(out, diffSection(section, types, k)...)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Key < out[b].Key })
	return out
}

// topSections 返回配置树的顶级段名。
func topSections(k *koanf.Koanf) []string {
	seen := map[string]struct{}{}
	for _, key := range k.Keys() {
		seen[topSegment(key)] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// UnusedKey 是 [Config.Unused] 返回的单条结果。
type UnusedKey struct {
	Key    string
	Reason string
}

// diffSection 比对「配置里存在的键」与「任一消费方结构体的 yaml tag」。
func diffSection(section string, types []reflect.Type, k *koanf.Koanf) []UnusedKey {
	declared := map[string]struct{}{}
	names := make([]string, 0, len(types))
	for _, typ := range types {
		for field := range yamlFields(typ) {
			declared[field] = struct{}{}
		}
		names = append(names, typ.String())
	}
	prefix := normalizeSection(section)

	var out []UnusedKey
	for _, key := range k.Keys() {
		if !strings.HasPrefix(key, prefix+".") {
			continue
		}
		rest := strings.TrimPrefix(key, prefix+".")
		if _, ok := declared[rest]; ok {
			continue
		}
		out = append(out, UnusedKey{
			Key:    key,
			Reason: fmt.Sprintf("%v has no matching yaml field", names),
		})
	}
	return out
}

// yamlFields 收集结构体（含内嵌）的 yaml 字段路径集合。
func yamlFields(t reflect.Type) map[string]struct{} {
	out := map[string]struct{}{}
	collectYAMLFields(t, "", out, 0)
	return out
}

func collectYAMLFields(t reflect.Type, prefix string, out map[string]struct{}, depth int) {
	if depth > 4 {
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := yamlFieldName(f)
		if name == "" {
			continue
		}
		full := name
		if prefix != "" {
			full = prefix + "." + name
		}
		out[full] = struct{}{}

		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && ft != reflect.TypeFor[time.Time]() {
			collectYAMLFields(ft, full, out, depth+1)
		}
	}
}

// yamlFieldName 读取 yaml tag；返回 "" 表示该字段不参与配置。
func yamlFieldName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("yaml")
	if !ok {
		// 没有 yaml tag 时，koanf 会按字段名小写匹配；保守地认为是参与配置的。
		return strings.ToLower(f.Name)
	}
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return ""
	}
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

func topSegment(key string) string {
	if idx := strings.Index(key, "."); idx >= 0 {
		return key[:idx]
	}
	return key
}

func normalizeSection(key string) string {
	return strings.Trim(strings.TrimSpace(key), ".")
}
