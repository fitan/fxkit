package di

import (
	"reflect"

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
func Invoke[T any](i Injector) (T, error) {
	return do.Invoke[T](i)
}

// MustInvoke 从容器中强制解析类型 T，失败时 panic。
func MustInvoke[T any](i Injector) T {
	return do.MustInvoke[T](i)
}

// RootScope 包装根注入容器。
type RootScope = do.RootScope

// New 创建一个全新的 RootScope，并注入内置核心服务（如 Lifecycle）。
func New(opts ...Option) *RootScope {
	root := do.New()
	lc := NewLifecycle()
	do.ProvideValue(root, lc)

	for _, opt := range opts {
		if opt != nil {
			opt(root)
		}
	}
	return root
}

// ToOption 将任意函数适配为 Option。
func ToOption(fn any) Option {
	if fn == nil {
		return nil
	}
	if opt, ok := fn.(Option); ok {
		return opt
	}
	if opt, ok := fn.(func(Injector)); ok {
		return opt
	}

	val := reflect.ValueOf(fn)
	if val.Kind() != reflect.Func {
		return func(i Injector) {
			do.ProvideValue(i, fn)
		}
	}

	// 统一处理为反射解析参数并调用的 Provider 包装
	return func(i Injector) {
		typ := val.Type()
		if typ.NumOut() == 0 {
			// 普通执行函数
			args := make([]reflect.Value, typ.NumIn())
			for idx := 0; idx < typ.NumIn(); idx++ {
				argType := typ.In(idx)
				if argType == reflect.TypeOf((*Lifecycle)(nil)) {
					lc, _ := do.Invoke[*Lifecycle](i)
					args[idx] = reflect.ValueOf(lc)
				}
			}
			val.Call(args)
		}
	}
}
