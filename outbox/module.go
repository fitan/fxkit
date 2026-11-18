package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
)

// Module 装配 [Store]、[Inbox]，并在 outbox.enabled 为 true 时迁移表并启动 [Relay]。
// 已包含在 [github.com/fitan/fxkit.Default] 中；outbox.enabled=true 之前 relay 保持关闭。
// Publisher 是来自 hatchetx 的 [EventPublisher]（hatchet.outbox_publisher）或 [ProvidePublisher]。
var Module = di.Module("fxkit/outbox",
	config.Provide[Config]("outbox"),
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*Store, error) {
			client, _ := di.Invoke[*gormx.Client](i)
			return NewStore(client), nil
		})
		di.Provide(i, func(i di.Injector) (*Inbox, error) {
			client, _ := di.Invoke[*gormx.Client](i)
			cfg, _ := di.Invoke[*Config](i)
			timeout := defaultInboxClaimTimeout
			if cfg != nil && cfg.InboxClaimTimeout > 0 {
				timeout = cfg.InboxClaimTimeout
			}
			return NewInboxWithTimeout(client, timeout), nil
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

		// 登记 schema 契约：仅当 outbox 启用时才要求这两张表存在。
		// gormx 不再硬编码它们，只用 gormx 的服务不会因为缺表而启动失败。
		if cfg, err := di.Invoke[*Config](i); err == nil && cfg != nil && cfg.Enabled {
			gormx.RegisterSchemaContract("outbox_events",
				"id", "pubsub", "topic", "payload", "idempotency_key", "status",
				"attempts", "last_error", "lease_id", "locked_at", "next_attempt_at",
				"created_at", "published_at",
			)
			gormx.RegisterSchemaContract("outbox_inbox_events",
				"id", "topic", "status", "claimed_at", "lease_id", "processed_at")
		}

		lc := di.MustInvoke[*di.Lifecycle](i)
		registerLifecycle(lc, i)
	},
)

func registerLifecycle(lc *di.Lifecycle, inj di.Injector) {
	if lc == nil || inj == nil {
		return
	}

	var relay *Relay
	var purgeCancel context.CancelFunc

	lc.Append(di.Hook{
		OnStart: func(ctx context.Context) error {
			// 在 OnStart 里解析配置 / 客户端 / Relay：
			// Relay 构造时会读取 EventPublisher group，而 group 只有在全部模块装配完成后
			// 才完整（早期在装配期读取，宿主模块注册的 publisher 会被静默丢弃）。
			cfg, err := di.Invoke[*Config](inj)
			if err != nil {
				return fmt.Errorf("outbox: resolve config: %w", err)
			}
			if cfg == nil {
				return nil
			}

			client, err := di.Invoke[*gormx.Client](inj)
			if err != nil {
				return fmt.Errorf("outbox: resolve db client: %w", err)
			}

			if cfg.PurgeInterval > 0 {
				if client == nil || !client.Enabled() {
					return fmt.Errorf("outbox.purge_interval requires a configured database (db.driver/db.dsn)")
				}
				store, err := di.Invoke[*Store](inj)
				if err != nil {
					return fmt.Errorf("outbox: resolve store: %w", err)
				}
				inbox, _ := di.Invoke[*Inbox](inj)
				var locker consulx.Locker
				if l, lerr := di.Invoke[consulx.Locker](inj); lerr == nil {
					locker = l
				}
				pctx, cancel := context.WithCancel(context.Background())
				purgeCancel = cancel
				go runPurgeLoop(pctx, store, inbox, cfg, locker)
			}

			if !cfg.Enabled {
				return nil
			}
			if client == nil || !client.Enabled() {
				return fmt.Errorf("outbox.enabled=true requires a configured database (db.driver/db.dsn)")
			}

			r, err := di.Invoke[*Relay](inj)
			if err != nil {
				return fmt.Errorf("outbox: resolve relay: %w", err)
			}
			if r == nil {
				return nil
			}
			if r.Publisher() == nil {
				return fmt.Errorf("outbox.enabled=true requires an EventPublisher (hatchet.outbox_publisher or outbox.ProvidePublisher)")
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
			if purgeCancel != nil {
				purgeCancel()
			}
			if relay == nil {
				return nil
			}
			relay.Stop()
			slog.Info("outbox relay stopped")
			return nil
		},
	})
}

func runPurgeLoop(ctx context.Context, store *Store, inbox *Inbox, cfg *Config, locker consulx.Locker) {
	if cfg == nil || cfg.PurgeInterval <= 0 {
		return
	}
	ticker := time.NewTicker(cfg.PurgeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runPurgeOnce(ctx, store, inbox, cfg, locker)
		}
	}
}

// runPurgeOnce 优先用 Consul 锁保证同一时刻只有一个副本清理；没有锁时退化为单进程执行。
func runPurgeOnce(ctx context.Context, store *Store, inbox *Inbox, cfg *Config, locker consulx.Locker) {
	if store == nil {
		return
	}
	purge := func(ctx context.Context) error {
		now := time.Now().UTC()
		if cfg.PurgeBefore > 0 {
			if n, err := store.PurgePublished(ctx, now.Add(-cfg.PurgeBefore)); err != nil {
				slog.Warn("outbox purge published", "error", err)
			} else if n > 0 {
				slog.Info("outbox purged published", "rows", n)
			}
			if n, err := store.PurgeFailed(ctx, now.Add(-cfg.PurgeBefore)); err != nil {
				slog.Warn("outbox purge failed", "error", err)
			} else if n > 0 {
				slog.Info("outbox purged failed", "rows", n)
			}
		}
		if inbox != nil && cfg.InboxPurgeBefore > 0 {
			if n, err := inbox.PurgeDone(ctx, now.Add(-cfg.InboxPurgeBefore)); err != nil {
				slog.Warn("outbox purge inbox", "error", err)
			} else if n > 0 {
				slog.Info("outbox purged inbox", "rows", n)
			}
		}
		return nil
	}
	if locker != nil {
		lockCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := locker.WithLock(lockCtx, "fxkit:outbox:purge", purge); err != nil {
			if errors.Is(err, consulx.ErrConsulDisabled) {
				if perr := purge(ctx); perr != nil {
					slog.Warn("outbox purge", "error", perr)
				}
				return
			}
			slog.Debug("outbox purge skipped", "error", err)
		}
		return
	}
	if err := purge(ctx); err != nil {
		slog.Warn("outbox purge", "error", err)
	}
}
