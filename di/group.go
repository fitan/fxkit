package di

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/samber/do/v2"
)

// Group 用于汇聚同一类型 T 的多个提供者（替代 Fx 的 fx.Group / ResultTags）。
type Group[T any] struct {
	mu    sync.Mutex
	items []T
}

// NewGroup 创建一个新的 Group[T]。
func NewGroup[T any]() *Group[T] {
	return &Group[T]{
		items: make([]T, 0),
	}
}

// Add 追加一个或多个条目。
func (g *Group[T]) Add(items ...T) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.items = append(g.items, items...)
}

// Items 返回当前组内已收集的所有条目副本。
func (g *Group[T]) Items() []T {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]T(nil), g.items...)
}

// groupKey 用于按「容器 + 元素类型」定位 group。
//
// 用容器 ID 字符串而不是 Injector 接口值作键，原因有二：
//
//  1. 归一化：samber/do 解析服务时把 `virtualScope`（循环依赖检测用）作为
//     Injector 传给 provider，它与注册时的 *RootScope 不是同一个值。
//     直接拿来当 key 会导致 group 查不到（表现为 "could not find service []T"）。
//  2. 不持有引用：接口值作 map key 会让 registry 永久持有已 Shutdown 的容器
//     （实测 100 个容器泄漏 100 条记录）。ID 是纯字符串，容器可被 GC。
type groupKey struct {
	owner string
	elem  reflect.Type
}

var (
	groupRegistryMu sync.RWMutex
	// groupRegistry 记录每个容器里 group 的「元素类型 → 取条目函数」。
	groupRegistry = map[groupKey]func() []any{}

	// strictWiring 开启后，装配期（Option 执行中）读 group 会直接 panic。
	// 这是本次审查中最多的一类隐式故障（读到的只是「已装配部分」）：
	// 用 FXKIT_STRICT_WIRING=1 或 [SetStrictWiring] 在 CI 里把问题变成硬失败。
	strictWiring atomic.Bool

	// assemblingOwners 记录正在执行 Option 的容器。
	assemblingOwners sync.Map // ownerID -> struct{}
)

func init() {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FXKIT_STRICT_WIRING"))) {
	case "1", "true", "yes", "on":
		strictWiring.Store(true)
	}
}

// SetStrictWiring 开启/关闭装配期 group 读取检查（主要给测试与 CI 用）。
func SetStrictWiring(on bool) { strictWiring.Store(on) }

// StrictWiringEnabled 返回当前是否开启严格装配检查。
func StrictWiringEnabled() bool { return strictWiring.Load() }

// markAssembling 标记容器正在执行 Option，返回解除函数。
func markAssembling(i Injector) func() {
	id := ownerID(i)
	if id == "" {
		return func() {}
	}
	assemblingOwners.Store(id, struct{}{})
	return func() { assemblingOwners.Delete(id) }
}

// checkAssemblyRead 在严格模式下禁止装配期读取 group。
func checkAssemblyRead(i Injector, op string) {
	if !strictWiring.Load() {
		return
	}
	if _, ok := assemblingOwners.Load(ownerID(i)); ok {
		panic(fmt.Sprintf("%s: group read during graph assembly; read groups from a Lifecycle PreStart hook instead "+
			"(set FXKIT_STRICT_WIRING=0 to disable this check)", op))
	}
}

// ownerID 把任意 injector（含 virtualScope 包装）归一化为其 RootScope 的稳定 ID。
func ownerID(i Injector) string {
	if i == nil {
		return ""
	}
	if rs := i.RootScope(); rs != nil {
		return rs.ID()
	}
	return i.ID()
}

// ReleaseInjector 清理某个容器在 group 注册表中留下的记录。
//
// 容器 Shutdown 后应调用它（[New] 返回的 RootScope 会在包一层 Shutdown 时自动调用）。
// 不调用只会造成少量内存滞留，不影响正确性。
func ReleaseInjector(i Injector) {
	if i == nil {
		return
	}
	id := ownerID(i)
	groupRegistryMu.Lock()
	for k := range groupRegistry {
		if k.owner == id {
			delete(groupRegistry, k)
		}
	}
	groupRegistryMu.Unlock()
}

// registerGroup 登记 group，使参数结构体里的 []T 字段可以从 *Group[T] 取值。
func registerGroup[T any](i Injector, g *Group[T]) {
	groupRegistryMu.Lock()
	defer groupRegistryMu.Unlock()
	groupRegistry[groupKey{owner: ownerID(i), elem: reflect.TypeFor[T]()}] = func() []any {
		items := g.Items()
		out := make([]any, len(items))
		for idx, v := range items {
			out[idx] = v
		}
		return out
	}
}

// groupValues 返回容器 i 中元素类型为 elem 的 group 全部条目。
func groupValues(i Injector, elem reflect.Type) ([]any, bool) {
	groupRegistryMu.RLock()
	fn, ok := groupRegistry[groupKey{owner: ownerID(i), elem: elem}]
	groupRegistryMu.RUnlock()
	if !ok {
		return nil, false
	}
	return fn(), true
}

// ProvideGroupItem 向 Injector 中类型为 T 的 Group 注册一个条目。
func ProvideGroupItem[T any](i Injector, item T) {
	ProvideGroupItems(i, item)
}

// ProvideGroupItems 向 Injector 中类型为 T 的 Group 注册多个条目。
func ProvideGroupItems[T any](i Injector, items ...T) {
	g, err := do.Invoke[*Group[T]](i)
	if err != nil || g == nil {
		g = NewGroup[T]()
		do.ProvideValue(i, g)
	}
	registerGroup(i, g)
	g.Add(items...)
}

// GetGroup 从 Injector 中获取已注册的 Group[T] 中所有条目。若未注册则返回 nil。
//
// 注意：group 是容器内可变状态，只能在装配完成后（Lifecycle.PreStart）读取；
// 在模块的 Option 闭包里读只能拿到「已装配部分」。开启严格装配时这种读法会 panic。
func GetGroup[T any](i Injector) []T {
	checkAssemblyRead(i, "di.GetGroup")
	g, err := do.Invoke[*Group[T]](i)
	if err != nil || g == nil {
		return nil
	}
	return g.Items()
}

// ListGroups 返回容器 i 中已登记的 group 元素类型名，便于排查「group 为空」类问题。
func ListGroups(i Injector) []string {
	checkAssemblyRead(i, "di.ListGroups")
	types := listGroupTypes(i)
	out := make([]string, len(types))
	for idx, t := range types {
		out[idx] = t.String()
	}
	return out
}

// listGroupTypes 返回容器 i 中已登记的 group 元素类型（按类型名排序）。
func listGroupTypes(i Injector) []reflect.Type {
	groupRegistryMu.RLock()
	defer groupRegistryMu.RUnlock()
	var out []reflect.Type
	owner := ownerID(i)
	for k := range groupRegistry {
		if k.owner == owner {
			out = append(out, k.elem)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].String() < out[b].String() })
	return out
}
