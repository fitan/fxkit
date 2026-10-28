package di

import (
	"sync"

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
	g.Add(items...)
}

// GetGroup 从 Injector 中获取已注册的 Group[T] 中所有条目。若未注册则返回 nil。
func GetGroup[T any](i Injector) []T {
	g, err := do.Invoke[*Group[T]](i)
	if err != nil || g == nil {
		return nil
	}
	return g.Items()
}
