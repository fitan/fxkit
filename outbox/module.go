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
// Publisher is Hatchet when hatchet.outbox_publisher is true — see [NewRelay].
var Module = fx.Module("fxkit/outbox",
	fx.Provide(NewStore),
	fx.Provide(NewInbox),
	fx.Provide(NewRelay),
	fx.Invoke(registerLifecycle),
)

func registerLifecycle(lc fx.Lifecycle, cfg *config.Config, client *gormx.Client, relay *Relay, inbox *Inbox) {
	if cfg == nil || !cfg.Get().Outbox.Enabled {
		return
	}
	if client == nil {
		slog.Warn("outbox enabled but gormx client is nil; relay not started")
		return
	}
	if relay == nil || relay.publisher == nil {
		slog.Warn("outbox enabled but no Hatchet publisher (set hatchet.enabled + outbox_publisher); relay not started")
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
				"poll_interval", cfg.Get().Outbox.PollDuration(),
				"batch_size", cfg.Get().Outbox.BatchSize,
				"max_retries", cfg.Get().Outbox.MaxRetries,
				"claim_timeout", cfg.Get().Outbox.ClaimTimeoutDuration(),
			)
			go relay.Run(context.Background())
			return nil
		},
		OnStop: func(_ context.Context) error {
			relay.Stop()
			slog.Info("outbox relay stopped")
			return nil
		},
	})
}
