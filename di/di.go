// Package di 是 fxkit 的依赖注入与生命周期装配层，底层复用 samber/do/v2。
//
// 用 [New] 创建根容器，通过 [Module]、[Provide]、[ProvideValue] 注册服务，
// 用 [Invoke] / [MustInvoke] 解析；[Lifecycle] 按优先级分阶段执行 PreStart 与
// OnStart/OnStop 钩子，[Lifecycle.Start] 失败时会逆序回滚已启动的钩子。
package di

import (
	"fmt"

	"github.com/samber/do/v2"
)

// Injector 是依赖注入容器接口，直接复用 samber/do/v2.Injector。
type Injector = do.Injector

// Provider 是类型安全的构造函数。
type Provider[T any] = do.Provider[T]

// Option 是模块或配置的装配函数。
type Option func(Injector)

// Module 将一组 Option 命名组合为一个 Option。
func Module(name string, opts ...Option) Option {
	return func(i Injector) {
		for _, opt := range opts {
			if opt != nil {
				opt(i)
			}
		}
	}
}

// Options 将多个 Option 组合成单个 Option。
func Options(opts ...Option) Option {
	return func(i Injector) {
		for _, opt := range opts {
			if opt != nil {
				opt(i)
			}
		}
	}
}

// Provide 向容器中注册一个 Provider。
func Provide[T any](i Injector, provider Provider[T]) {
	do.Provide(i, provider)
}

// ProvideValue 向容器中注册一个已有的值。
func ProvideValue[T any](i Injector, value T) {
	do.ProvideValue(i, value)
}

// ProvideNamed 向容器中注册一个带名称的 Provider。
func ProvideNamed[T any](i Injector, name string, provider Provider[T]) {
	do.ProvideNamed(i, name, provider)
}

// Invoke 从容器中解析类型 T。
//
// 只有一条路径：samber/do 按类型精确匹配（含接口别名，并保留循环依赖检测）。
// 早期还有一个「按类型名取 any 再断言」的回退路径，那是为反射注册的服务准备的；
// 现在所有服务都类型化注册，回退路径已删除。
func Invoke[T any](i Injector) (T, error) {
	var zero T
	if i == nil {
		return zero, fmt.Errorf("di: nil injector resolving %T", zero)
	}
	return do.Invoke[T](i)
}

// MustInvoke 从容器中强制解析类型 T，失败时 panic。
func MustInvoke[T any](i Injector) T {
	v, err := Invoke[T](i)
	if err != nil {
		panic(err)
	}
	return v
}

// RootScope 包装根注入容器。
type RootScope = do.RootScope

// Shutdown 关闭容器。
//
// 等价于 `root.Shutdown()`；保留本函数是因为早期版本需要额外清理包级 group 注册表，
// 现在 group 索引已经挂在容器上，不再需要任何全局清理。
func Shutdown(root *RootScope) *do.ShutdownReport {
	if root == nil {
		return nil
	}
	return root.Shutdown()
}

// New 创建一个全新的 RootScope，并注入内置核心服务（如 Lifecycle 与 group 索引）。
func New(opts ...Option) *RootScope {
	root := do.New()

	lc := NewLifecycle()
	do.ProvideValue(root, lc)

	gi := newGroupIndex()
	do.ProvideValue(root, gi)

	// 标记装配区间：严格装配模式下，此时读 group 会 panic。
	gi.assembling.Store(true)
	defer gi.assembling.Store(false)

	for _, opt := range opts {
		if opt != nil {
			opt(root)
		}
	}
	return root
}
