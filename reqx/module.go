package reqx

import "go.uber.org/fx"

// Module provides [Factory] and stops Consul watches on Fx shutdown.
// Included in [github.com/fitan/fxkit.Default].
var Module = fx.Module("fxkit/reqx",
	fx.Provide(NewFactory),
)
