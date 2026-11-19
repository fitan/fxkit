package fxkit

import (
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
)

// ServiceOption 用于定制 [Service] 声明。
type ServiceOption struct {
	// options 是该项贡献给宿主模块的 di.Option 列表。
	options []di.Option
}

// Service 声明一个宿主模块：若干显式注册项与贡献点。
//
// 与早期版本的区别：不再接受任意签名（`Service(name, NewService)`），
// 因为那需要反射解析构造函数。现在每一项都必须显式声明依赖来源：
//
//	fxkit.Service("article",
//	    fxkit.Provide(func(i di.Injector) (*Service, error) {
//	        return NewService(di.MustInvoke[*gormx.Client](i))
//	    }),
//	    fxkit.Routes(articleRoutes...),
//	)
//
// 好处是接线错误在编译期暴露，而不是启动期 panic。
func Service(name string, opts ...ServiceOption) di.Option {
	all := make([]di.Option, 0, len(opts))
	for _, opt := range opts {
		all = append(all, opt.options...)
	}
	return di.Module(name, all...)
}

// Provide 注册一个类型安全的构造函数。
func Provide[T any](ctor func(di.Injector) (T, error)) ServiceOption {
	return ServiceOption{options: []di.Option{
		func(i di.Injector) { di.Provide(i, ctor) },
	}}
}

// ProvideValue 向容器直接注册一个已有实例。
func ProvideValue[T any](val T) ServiceOption {
	return ServiceOption{options: []di.Option{
		func(i di.Injector) { di.ProvideValue(i, val) },
	}}
}

// ProvideConfig 把 YAML 顶层 key 解成 *T 并注入。
func ProvideConfig[T any](key string) ServiceOption {
	return ServiceOption{options: []di.Option{config.Provide[T](key)}}
}

// Routes 贡献若干 [server.Route]。
func Routes(routes ...server.Route) ServiceOption {
	return ServiceOption{options: []di.Option{server.ProvideRoutes(routes...)}}
}

// RoutesLazy 在 PreStart 阶段构造路由（可访问完整依赖图）。
func RoutesLazy(fn func(di.Injector) ([]server.Route, error)) ServiceOption {
	return ServiceOption{options: []di.Option{server.ProvideRoutesLazy(fn)}}
}

// Invoke 注册一个在应用启动时（Lifecycle.OnStart）执行的副作用函数。
// 返回 error 时中止启动。
//
//	fxkit.Invoke(func(i di.Injector) error {
//	    return di.MustInvoke[*OrderService](i).Warmup()
//	})
func Invoke(fn func(di.Injector) error) ServiceOption {
	return ServiceOption{options: []di.Option{di.InvokeOption(fn)}}
}
