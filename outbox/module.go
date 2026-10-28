package outbox

import (
	"context"
	"log/slog"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/di"
)

// Module wires [Store], [Inbox], and when outbox.enabled is true, migrates tables and starts [Relay].
// Included in [github.com/fitan/fxkit.Default]; relay stays off until outbox.enabled=true.
// Publisher is an [EventPublisher] from hatchetx (hatchet.outbox_publisher) or [ProvidePublisher].
var Module = di.Module("fxkit/outbox",
	config.Provide[Config]("outbox"),
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*Store, error) {
			client, _ := di.Invoke[*gormx.Client](i)
			return NewStore(client), nil
		})
		di.Provide(i, func(i di.Injector) (*Inbox, error) {
			client, _ := di.Invoke[*gormx.Client](i)
			return NewInbox(client), nil
		})
		di.Provide(i, func(i di.Injector) (*Relay, error) {
			client, _ := di.Invoke[*gormx.Client](i)
			cfg, _ := di.Invoke[*Config](i)
			pubs := di.GetGroup[EventPublisher](i)
			return NewRelay(NewRelayParams{
				Client:     client,
				Outbox:     cfg,
				Publishers: pubs,
			}), nil
		})

		lc, _ := di.Invoke[*di.Lifecycle](i)
		cfg, _ := di.Invoke[*Config](i)
		client, _ := di.Invoke[*gormx.Client](i)
		relay, _ := di.Invoke[*Relay](i)
		inbox, _ := di.Invoke[*Inbox](i)
		registerLifecycle(lc, cfg, client, relay, inbox)
	},
)

func registerLifecycle(lc *di.Lifecycle, cfg *Config, client *gormx.Client, relay *Relay, inbox *Inbox) {
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

	if lc != nil {
		lc.Append(di.Hook{
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
}
