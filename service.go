package fxkit

import (
	"github.com/fitan/fxkit/server"
	"go.uber.org/fx"
)

// ServiceOption 用于定制 [Service] 声明。请始终通过辅助函数（[Routes] 等）构造。
type ServiceOption struct {
	options []fx.Option
}

// Service 声明一个宿主模块：单一主构造函数及若干 fx group 贡献项。
//
//	var Module = fxkit.Service("users", NewService,
//	    fxkit.Routes(newRoutes),
//	)
func Service(name string, ctor any, opts ...ServiceOption) fx.Option {
	all := []fx.Option{fx.Provide(ctor)}
	for _, opt := range opts {
		all = append(all, opt.options...)
	}
	return fx.Module(name, all...)
}

// Provide 包装 fx.Provide。
func Provide(ctors ...any) ServiceOption {
	out := make([]fx.Option, 0, len(ctors))
	for _, c := range ctors {
		out = append(out, fx.Provide(c))
	}
	return ServiceOption{options: out}
}

// Invoke 包装 fx.Invoke。
func Invoke(fns ...any) ServiceOption {
	out := make([]fx.Option, 0, len(fns))
	for _, f := range fns {
		out = append(out, fx.Invoke(f))
	}
	return ServiceOption{options: out}
}

// Routes 从构造函数贡献 []server.Route —— 等价于 [server.ProvideRoutes]。
func Routes(fn any) ServiceOption {
	return ServiceOption{options: []fx.Option{server.ProvideRoutes(fn)}}
}
