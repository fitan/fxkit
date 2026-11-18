package audit

import (
	"context"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/outbox"
	"github.com/fitan/fxkit/server"
)

// Module 是 opt-in 审计模块。放在 authz.Module 之后，确保拿到 Actor/OrgID。
var Module = di.Module("fxkit/audit",
	config.Provide[Config]("audit"),
	server.ProvideMiddlewareLazy(func(i di.Injector) (server.Middleware, error) {
		cfg, err := di.Invoke[*Config](i)
		if err != nil {
			return nil, err
		}
		if cfg == nil || !cfg.Enabled {
			return nil, nil
		}
		sink, err := provideSink(i, cfg)
		if err != nil {
			return nil, err
		}
		return Middleware(*cfg, sink), nil
	}),
	func(i di.Injector) {
		cfg, err := di.Invoke[*Config](i)
		if err != nil || cfg == nil || !cfg.Enabled || cfg.Sink != "db" {
			return
		}
		gormx.RegisterSchemaContract("audit_events",
			"id", "at", "actor", "org_id", "method", "path", "status",
			"action", "resource", "request_id", "trace_id", "ip", "metadata")
		lc, err := di.Invoke[*di.Lifecycle](i)
		if err != nil || lc == nil {
			return
		}
		lc.Append(di.Hook{
			OnStart: func(ctx context.Context) error {
				client, err := di.Invoke[*gormx.Client](i)
				if err != nil || client == nil || !client.Enabled() {
					return fxerrors.Internal("audit: sink=db requires a configured database")
				}
				if !client.AutoMigrate() {
					return nil
				}
				return NewDBSink(client).Migrate(ctx)
			},
		})
	},
)

func provideSink(i di.Injector, cfg *Config) (Sink, error) {
	switch cfg.Sink {
	case "", "slog":
		return SlogSink{}, nil
	case "db":
		client, err := di.Invoke[*gormx.Client](i)
		if err != nil || client == nil || !client.Enabled() {
			return nil, fxerrors.Internal("audit: sink=db requires a configured database")
		}
		return NewDBSink(client), nil
	case "outbox":
		store, err := di.Invoke[*outbox.Store](i)
		if err != nil || store == nil {
			return nil, fxerrors.Internal("audit: sink=outbox requires outbox.Module")
		}
		return NewOutboxSink(store, cfg.Topic), nil
	default:
		return nil, fxerrors.Internal("audit: unknown sink %q", cfg.Sink)
	}
}
