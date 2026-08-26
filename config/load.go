package config

import (
	"fmt"

	"go.uber.org/fx"
)

// Load 把 YAML 顶层 key 解到 T（yaml tag）。
// 若 T 实现 SetDefaults()，先填默认再 overlay；实现 Validate() error 则校验，失败直接返回错误。
// key 缺失时保留默认值（或零值），不报错。
//
//	type OrdersConfig struct {
//	    PageSize int `yaml:"page_size"`
//	}
//	c, err := config.Load[OrdersConfig](cfg, "orders")
func Load[T any](cfg *Config, key string) (*T, error) {
	var v T
	if cfg == nil {
		return nil, fmt.Errorf("config load %q: nil Config", key)
	}
	if d, ok := any(&v).(interface{ SetDefaults() }); ok {
		d.SetDefaults()
	}
	if err := cfg.UnmarshalKey(key, &v); err != nil {
		return nil, fmt.Errorf("config load %q: %w", key, err)
	}
	if val, ok := any(&v).(interface{ Validate() error }); ok {
		if err := val.Validate(); err != nil {
			return nil, fmt.Errorf("config %s: %w", key, err)
		}
	}
	return &v, nil
}

// Provide 把 Load[T](key) 注册进 Fx，供构造函数注入 *T。校验失败则 Fx 启动失败。
//
//	fxkit.Run(fxkit.Default(), config.Provide[OrdersConfig]("orders"), orders.Module)
func Provide[T any](key string) fx.Option {
	return fx.Provide(func(cfg *Config) (*T, error) {
		return Load[T](cfg, key)
	})
}
