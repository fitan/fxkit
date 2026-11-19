package idempotency

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/server"
)

// Module 是 opt-in 幂等键模块。放在 authz.Module 之后。
var Module = di.Module("fxkit/idempotency",
	config.Provide[Config]("idempotency"),
	server.ProvideMiddlewareLazy(func(i di.Injector) (server.Middleware, error) {
		cfg, err := di.Invoke[*Config](i)
		if err != nil {
			return nil, err
		}
		if cfg == nil || !cfg.Enabled {
			return nil, nil
		}
		client, err := di.Invoke[*gormx.Client](i)
		if err != nil || client == nil || !client.Enabled() {
			return nil, fxerrors.Internal("idempotency: enabled requires a configured database")
		}
		return Middleware(*cfg, NewDBStore(client)), nil
	}),
	func(i di.Injector) {
		cfg, err := di.Invoke[*Config](i)
		if err != nil || cfg == nil || !cfg.Enabled {
			return
		}
		gormx.RegisterSchemaContract("idempotency_keys",
			"key", "request_hash", "status_code", "content_type", "response_body",
			"completed", "created_at", "expires_at")
		lc, err := di.Invoke[*di.Lifecycle](i)
		if err != nil || lc == nil {
			return
		}
		var purgeCancel context.CancelFunc
		lc.Append(di.Hook{
			OnStart: func(ctx context.Context) error {
				client, err := di.Invoke[*gormx.Client](i)
				if err != nil || client == nil || !client.Enabled() {
					return fxerrors.Internal("idempotency: enabled requires a configured database")
				}
				store := NewDBStore(client)
				if client.AutoMigrate() {
					if err := store.Migrate(ctx); err != nil {
						return err
					}
				}
				if cfg.PurgeInterval > 0 {
					var locker consulx.Locker
					if l, lerr := di.Invoke[consulx.Locker](i); lerr == nil {
						locker = l
					}
					pctx, cancel := context.WithCancel(context.Background())
					purgeCancel = cancel
					go runPurgeLoop(pctx, store, cfg.PurgeInterval, locker)
				}
				return nil
			},
			OnStop: func(context.Context) error {
				if purgeCancel != nil {
					purgeCancel()
				}
				return nil
			},
		})
	},
)

// runPurgeLoop 按 interval 周期调用 runPurgeOnce，直到 ctx 结束。
func runPurgeLoop(ctx context.Context, store *DBStore, interval time.Duration, locker consulx.Locker) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runPurgeOnce(ctx, store, locker)
		}
	}
}

// runPurgeOnce 删除过期幂等记录；有 Consul 锁时先抢锁，锁不可用时退化为单进程执行。
func runPurgeOnce(ctx context.Context, store *DBStore, locker consulx.Locker) {
	purge := func(ctx context.Context) error {
		n, err := store.PurgeExpired(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		if n > 0 {
			slog.Info("idempotency purged expired", "rows", n)
		}
		return nil
	}
	if locker != nil {
		lockCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := locker.WithLock(lockCtx, "fxkit:idempotency:purge", purge); err != nil {
			if errors.Is(err, consulx.ErrConsulDisabled) {
				if perr := purge(ctx); perr != nil {
					slog.Warn("idempotency purge", "error", perr)
				}
			}
		}
		return
	}
	if err := purge(ctx); err != nil {
		slog.Warn("idempotency purge", "error", err)
	}
}
