package fxkit

import (
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
)

// ServiceOption 用于定制 [Service] 声明。
type ServiceOption struct {
	options []di.Option
}

// Service 声明一个宿主模块：单一主构造函数及若干 group 贡献项。
func Service(name string, ctor any, opts ...ServiceOption) di.Option {
	all := []di.Option{}
	if opt := di.ToOption(ctor); opt != nil {
		all = append(all, opt)
	}
	for _, opt := range opts {
		all = append(all, opt.options...)
	}
	return di.Module(name, all...)
}

// Provide 包装任意构造函数或值提供者。
func Provide(ctors ...any) ServiceOption {
	out := make([]di.Option, 0, len(ctors))
	for _, c := range ctors {
		if opt := di.ToOption(c); opt != nil {
			out = append(out, opt)
		}
	}
	return ServiceOption{options: out}
}

// ProvideTyped 以完全静态强类型方式注册构造函数，无须反射包装。
// 利用泛型与反向类型推导，编译器自动推导出依赖注入类型 T。
func ProvideTyped[T any](ctor func(di.Injector) (T, error)) ServiceOption {
	return ServiceOption{
		options: []di.Option{
			func(i di.Injector) {
				di.Provide(i, ctor)
			},
		},
	}
}

// ProvideValue 向容器直接注册一个已有实例，具有编译期静态类型安全。
func ProvideValue[T any](val T) ServiceOption {
	return ServiceOption{
		options: []di.Option{
			func(i di.Injector) {
				di.ProvideValue(i, val)
			},
		},
	}
}

// ProvideConfig 把 YAML 顶层 key 解成 *T 并注入。
func ProvideConfig[T any](key string) ServiceOption {
	return ServiceOption{options: []di.Option{config.Provide[T](key)}}
}

// Routes 从构造函数贡献 []server.Route。
func Routes(fn any) ServiceOption {
	return ServiceOption{options: []di.Option{server.ProvideRoutes(fn)}}
}
