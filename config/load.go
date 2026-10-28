package config

import (
	"fmt"

	"github.com/fitan/fxkit/di"
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

// Provide 把 Load[T](key) 注册进 di，供构造函数注入 *T。校验失败则启动失败。
func Provide[T any](key string) di.Option {
	return func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*T, error) {
			cfg, err := di.Invoke[*Config](i)
			if err != nil {
				return nil, err
			}
			return Load[T](cfg, key)
		})
	}
}
