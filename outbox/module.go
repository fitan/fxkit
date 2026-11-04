package outbox

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
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
		registerLifecycle(lc, i)
	},
)

func registerLifecycle(lc *di.Lifecycle, inj di.Injector) {
	if lc == nil || inj == nil {
		return
	}

	var relay *Relay

	lc.Append(di.Hook{
		OnStart: func(ctx context.Context) error {
			// 在 OnStart 里解析配置 / 客户端 / Relay：
			// Relay 构造时会读取 EventPublisher group，而 group 只有在全部模块装配完成后
			// 才完整（早期在装配期读取，宿主模块注册的 publisher 会被静默丢弃）。
			cfg, err := di.Invoke[*Config](inj)
			if err != nil {
				return fmt.Errorf("outbox: resolve config: %w", err)
			}
			if cfg == nil || !cfg.Enabled {
				return nil
			}

			client, err := di.Invoke[*gormx.Client](inj)
			if err != nil {
				return fmt.Errorf("outbox: resolve db client: %w", err)
			}
			if client == nil {
				slog.Warn("outbox enabled but gormx client is nil; relay not started")
				return nil
			}

			r, err := di.Invoke[*Relay](inj)
			if err != nil {
				return fmt.Errorf("outbox: resolve relay: %w", err)
			}
			if r == nil {
				return nil
			}
			if r.publisher == nil {
				slog.Warn("outbox enabled but no EventPublisher (enable hatchet.outbox_publisher or outbox.ProvidePublisher); relay not started")
				return nil
			}

			if client.AutoMigrate() {
				if err := client.Conn(ctx).AutoMigrate(&OutboxEvent{}); err != nil {
					return err
				}
				inbox, _ := di.Invoke[*Inbox](inj)
				if inbox != nil {
					if err := inbox.Migrate(ctx); err != nil {
						return err
					}
				}
			} else {
				// 生产由 SQL 平台执行 gormx/migrations；这里只校验不建表。
				slog.Debug("outbox: auto_migrate disabled; DDL is owned by your SQL platform")
			}
			slog.Info("outbox relay starting",
				"poll_interval", cfg.PollInterval,
				"batch_size", cfg.BatchSize,
				"max_retries", cfg.MaxRetries,
				"claim_timeout", cfg.ClaimTimeout,
			)
			relay = r
			return r.Start(context.Background())
		},
		OnStop: func(_ context.Context) error {
			if relay == nil {
				return nil
			}
			relay.Stop()
			slog.Info("outbox relay stopped")
			return nil
		},
	})
}
