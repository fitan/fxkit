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
	// 登记消费方，使 Config.Unused() 能判断该段下哪些键是死配置。
	RegisterConsumer[T](key)
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
//
// 消费方在**注册时**就登记（而不是等到 Load 被调用），这样 `app validate`
// 即使没有执行任何延迟构造函数，也能知道该段有合法消费方，不会误报死键。
func Provide[T any](key string) di.Option {
	RegisterConsumer[T](key)
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
