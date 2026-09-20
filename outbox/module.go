package outbox

import (
	"context"
	"log/slog"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/gormx"
	"go.uber.org/fx"
)

// Module wires [Store], [Inbox], and when outbox.enabled is true, migrates tables and starts [Relay].
// Included in [github.com/fitan/fxkit.Default]; relay stays off until outbox.enabled=true.
// Publisher is an [EventPublisher] from hatchetx (hatchet.outbox_publisher) or [ProvidePublisher].
var Module = fx.Module("fxkit/outbox",
	config.Provide[Config]("outbox"),
	fx.Provide(NewStore),
	fx.Provide(NewInbox),
	fx.Provide(NewRelay),
	fx.Invoke(registerLifecycle),
)

func registerLifecycle(lc fx.Lifecycle, cfg *Config, client *gormx.Client, relay *Relay, inbox *Inbox) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	if client == nil {
		slog.Warn("outbox enabled but gormx client is nil; relay not started")
		return
	}
	if relay == nil || relay.publisher == nil {
		slog.Warn("outbox enabled but no EventPublisher (enable hatchet.outbox_publisher or outbox.ProvidePublisher); relay not started")
		return
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := client.Conn(ctx).AutoMigrate(&OutboxEvent{}); err != nil {
				return err
			}
			if inbox != nil {
				if err := inbox.Migrate(ctx); err != nil {
					return err
				}
			}
			slog.Info("outbox relay starting",
				"poll_interval", cfg.PollInterval,
				"batch_size", cfg.BatchSize,
				"max_retries", cfg.MaxRetries,
				"claim_timeout", cfg.ClaimTimeout,
			)
			return relay.Start(context.Background())
		},
		OnStop: func(_ context.Context) error {
			relay.Stop()
			slog.Info("outbox relay stopped")
			return nil
		},
	})
}
