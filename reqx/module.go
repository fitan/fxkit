package reqx

import (
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
)

// Module provides [Factory] and stops Consul watches on shutdown.
// Included in fxkit.Default.
var Module = di.Module("fxkit/reqx",
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*Factory, error) {
			lc, _ := di.Invoke[*di.Lifecycle](i)
			cfg, _ := di.Invoke[*config.Config](i)
			return NewFactory(lc, cfg), nil
		})
	},
)
