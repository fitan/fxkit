package di

import (
	"context"
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
	return &Group[T]{items: make([]T, 0)}
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

// groupIndex 是**容器级**的 group 索引：元素类型 → 取条目函数。
//
// 为什么需要它：Go 无法在运行期用 reflect.Type 动态实例化 *Group[T]，
// 所以参数结构体里的 []T 字段没法直接按类型从容器取。
//
// 为什么放在容器里而不是包级全局：早期用 `map[容器ID字符串]…` 做全局注册表，
// 导致必须手工清理（ReleaseInjector）、必须把 virtualScope 归一化成 RootScope.ID()，
// 而且容器是否被回收取决于调用方有没有记得清理。作为容器内的一个服务，
// 生命周期跟着容器走，这些问题全部消失。
type groupIndex struct {
	mu     sync.RWMutex
	byElem map[reflect.Type]func() []any

	// assembling 标记容器正在执行 Option 闭包（装配期）。
	// 严格模式下此时读 group 会 panic —— 读到的只是「已装配的部分」。
	assembling atomic.Bool
}

func newGroupIndex() *groupIndex {
	return &groupIndex{byElem: make(map[reflect.Type]func() []any)}
}

func (x *groupIndex) register(elem reflect.Type, items func() []any) {
	x.mu.Lock()
	x.byElem[elem] = items
	x.mu.Unlock()
}

func (x *groupIndex) items(elem reflect.Type) ([]any, bool) {
	x.mu.RLock()
	fn, ok := x.byElem[elem]
	x.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return fn(), true
}

func (x *groupIndex) exists(elem reflect.Type) bool {
	x.mu.RLock()
	_, ok := x.byElem[elem]
	x.mu.RUnlock()
	return ok
}

func (x *groupIndex) elemTypes() []reflect.Type {
	x.mu.RLock()
	out := make([]reflect.Type, 0, len(x.byElem))
	for t := range x.byElem {
		out = append(out, t)
	}
	x.mu.RUnlock()
	sort.Slice(out, func(a, b int) bool { return out[a].String() < out[b].String() })
	return out
}

// groupIndexOf 返回容器 i 的 group 索引；容器不是 di.New 建出来的则为 nil。
func groupIndexOf(i Injector) *groupIndex {
	if i == nil {
		return nil
	}
	gi, err := do.Invoke[*groupIndex](i)
	if err != nil {
		return nil
	}
	return gi
}

// strictWiring 开启后，装配期（Option 执行中）读 group 会直接 panic。
// 用 FXKIT_STRICT_WIRING=1 或 [SetStrictWiring] 在 CI 里把问题变成硬失败。
var strictWiring atomic.Bool

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

// checkAssemblyRead 在严格模式下禁止装配期读取 group。
func checkAssemblyRead(i Injector, op string) {
	if !strictWiring.Load() {
		return
	}
	if gi := groupIndexOf(i); gi != nil && gi.assembling.Load() {
		panic(fmt.Sprintf("%s: group read during graph assembly; read groups from a Lifecycle PreStart hook instead "+
			"(set FXKIT_STRICT_WIRING=0 to disable this check)", op))
	}
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
	if gi := groupIndexOf(i); gi != nil {
		gi.register(reflect.TypeFor[T](), func() []any {
			vals := g.Items()
			out := make([]any, len(vals))
			for idx, v := range vals {
				out[idx] = v
			}
			return out
		})
	}
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
	gi := groupIndexOf(i)
	if gi == nil {
		return nil
	}
	types := gi.elemTypes()
	out := make([]string, len(types))
	for idx, t := range types {
		out[idx] = t.String()
	}
	return out
}

// ProvideLazyE 把 fn 延迟到 [PhaseResolveContributors] 执行，并把结果写入 group。
//
// 与旧的 ProvideLazyGroupItem 相比，这里只接受 func(Injector) (T, error)：
// 依赖通过 di.MustInvoke 显式获取，因此不再需要反射解析任意函数签名。
func ProvideLazyE[T any](i Injector, fn func(Injector) (T, error)) {
	if fn == nil {
		return
	}
	lc := MustInvoke[*Lifecycle](i)
	lc.AppendPreStartPhase(PhaseResolveContributors, Hook{
		OnStart: func(context.Context) error {
			v, err := fn(i)
			if err != nil {
				return err
			}
			ProvideGroupItem(i, v)
			return nil
		},
	})
}

// ProvideLazy 是 [ProvideLazyE] 的无错误版本。
func ProvideLazy[T any](i Injector, fn func(Injector) T) {
	if fn == nil {
		return
	}
	ProvideLazyE(i, func(i Injector) (T, error) { return fn(i), nil })
}

// InvokeOption 在 OnStart 阶段执行一个副作用函数（参数自行从容器解析）。
//
//	fxkit.Invoke(func(i di.Injector) error { return di.MustInvoke[*Svc](i).Warmup() })
func InvokeOption(fn func(Injector) error) Option {
	if fn == nil {
		return nil
	}
	return func(i Injector) {
		lc := MustInvoke[*Lifecycle](i)
		lc.Append(Hook{OnStart: func(context.Context) error { return fn(i) }})
	}
}
