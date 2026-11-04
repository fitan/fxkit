package reqx

import (
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
)

// Module 提供 [Factory]，并在关闭时停止 Consul 监听。
// 已包含在 fxkit.Default 中。
var Module = di.Module("fxkit/reqx",
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*Factory, error) {
			lc, _ := di.Invoke[*di.Lifecycle](i)
			cfg, _ := di.Invoke[*config.Config](i)
			return NewFactory(lc, cfg), nil
		})
	},
)
