package system

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/cachex"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
)

// Config 是 catalog 段配置（system feature 自己消费的部分）。
type Config struct {
	// ReminderPeriod 是心跳提醒的 Cron 表达式。
	ReminderPeriod string `yaml:"reminder_period"`
}

// SetDefaults 设置提醒器默认 Cron（每 15 秒）。
func (c *Config) SetDefaults() {
	c.ReminderPeriod = "15s"
}

// Service 提供探针、cache 演示与两个 Hatchet actor。
type Service struct {
	counters   *repository.CounterRepo
	heartbeats *repository.HeartbeatRepo
	hatchet    *hatchetx.Client
	enforcer   *authz.Enforcer
	cfg        *Config
	cache      cachex.Cache
	counter    *hatchetx.Actor[IncInput, IncOutput]
	heartbeat  *hatchetx.Actor[HeartbeatIn, HeartbeatOut]
}

// NewService 装配依赖，并在启动时迁移表、seed reader 角色、注册 reminder。
func NewService(
	db *gormx.Client,
	counters *repository.CounterRepo,
	heartbeats *repository.HeartbeatRepo,
	ht *hatchetx.Client,
	enf *authz.Enforcer,
	cfg *Config,
	cache cachex.Cache,
	lc *di.Lifecycle,
) (*Service, error) {
	if db == nil {
		return nil, fxerrors.Internal("system: nil gormx client")
	}
	if counters == nil || heartbeats == nil {
		return nil, fxerrors.Internal("system: nil repository")
	}
	if cfg == nil {
		cfg = &Config{}
		cfg.SetDefaults()
	}
	s := &Service{
		counters:   counters,
		heartbeats: heartbeats,
		hatchet:    ht,
		enforcer:   enf,
		cfg:        cfg,
		cache:      cache,
	}
	counter, err := hatchetx.NewActor("catalog-counter", s.incCounter, hatchetx.WithActorDescription("article view counter"))
	if err != nil {
		return nil, err
	}
	s.counter = counter
	heartbeat, err := hatchetx.NewActor("catalog-heartbeat", s.onHeartbeat, hatchetx.WithActorDescription("reminder tick"))
	if err != nil {
		return nil, err
	}
	s.heartbeat = heartbeat
	lc.Append(di.Hook{
		OnStart: func(ctx context.Context) error {
			if err := entity.MigrateSystem(db.Conn(ctx)); err != nil {
				return err
			}
			if err := s.seedReader(ctx); err != nil {
				return err
			}
			s.maybeReminder(ctx)
			return nil
		},
	})
	return s, nil
}

// incCounter 是 catalog-counter actor 的处理器：可人为 hold 以观察并发/排队。
func (s *Service) incCounter(ctx context.Context, in IncInput) (IncOutput, error) {
	id := strings.TrimSpace(in.ActorID)
	if id == "" {
		return IncOutput{}, fxerrors.Validation(map[string]string{"actorId": "required"})
	}
	global, actorN, leave := counterInflight.enter(id)
	defer leave()
	if hold := capHold(in.HoldMs); hold > 0 {
		timer := time.NewTimer(hold)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return IncOutput{}, ctx.Err()
		case <-timer.C:
		}
	}
	delta := in.Delta
	if delta == 0 {
		delta = 1
	}
	value, err := s.counters.Increment(ctx, id, delta)
	if err != nil {
		return IncOutput{}, err
	}
	return IncOutput{Value: value, Inflight: global, ActorInflight: actorN}, nil
}

// onHeartbeat 是 catalog-heartbeat actor 的处理器：每次 tick 累加一次。
func (s *Service) onHeartbeat(ctx context.Context, in HeartbeatIn) (HeartbeatOut, error) {
	id := strings.TrimSpace(in.ActorID)
	if id == "" {
		id = "showcase"
	}
	row, err := s.heartbeats.Tick(ctx, id, time.Now().UTC())
	if err != nil {
		return HeartbeatOut{}, err
	}
	return HeartbeatOut{Ticks: row.Ticks, LastTick: row.LastTick}, nil
}

// seedReader 幂等地创建只读角色 bob，演示 Casbin 的自动初始化。
func (s *Service) seedReader(ctx context.Context) error {
	if s.enforcer == nil || !s.enforcer.Enabled() {
		return nil
	}
	if _, err := s.enforcer.EnsureRole(ctx, authz.CreateRoleInput{
		Name:        "reader",
		DisplayName: "reader",
		Description: "GET-only showcase role (bob)",
	}); err != nil {
		return err
	}
	if err := s.enforcer.SetRolePermissions(ctx, authz.SetRolePermissionsInput{
		Role: "reader",
		Items: []authz.RolePermission{
			{Path: "/articles", Method: http.MethodGet},
			{Path: "/articles/{id}", Method: http.MethodGet},
			{Path: "/comments", Method: http.MethodGet},
			{Path: "/me", Method: http.MethodGet},
			{Path: "/counters/{id}", Method: http.MethodGet},
			{Path: "/heartbeat", Method: http.MethodGet},
			{Path: "/article-fanout", Method: http.MethodGet},
		},
	}); err != nil {
		return err
	}
	return s.enforcer.AddSubjectRole(ctx, authz.AddSubjectRoleInput{Subject: "bob", Role: "reader"})
}

// maybeReminder 在 Hatchet 启用时注册周期 reminder；重复注册会被跳过。
func (s *Service) maybeReminder(ctx context.Context) {
	if s.hatchet == nil || !s.hatchet.Enabled() {
		return
	}
	period := strings.TrimSpace(s.cfg.ReminderPeriod)
	if period == "" {
		period = "*/15 * * * * *"
	}
	_, err := hatchetx.CreateReminder(ctx, s.hatchet, hatchetx.CreateReminderInput{
		WorkflowName: s.heartbeat.Name(),
		ActorID:      "showcase",
		ReminderName: "tick",
		Period:       period,
	})
	if err != nil {
		slog.Info("hatchetx reminder skipped", "error", err) // 副本重启时 Name+Expression 重复
	}
}

// getCounter 只读 DB，不经过 Hatchet（对比 incCounter）。
func (s *Service) getCounter(ctx context.Context, id string) (IncOutput, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return IncOutput{}, fxerrors.Validation(map[string]string{"id": "required"})
	}
	row, err := s.counters.Get(ctx, id)
	if err != nil {
		return IncOutput{}, err
	}
	return IncOutput{Value: row.Value}, nil
}

// cacheDemo 是最朴素的 Get/Set 演示；更完整的 Cache-Aside 见 demo 包的 GetOrSet。
func (s *Service) cacheDemo(ctx context.Context) (map[string]any, error) {
	if s.cache == nil {
		return map[string]any{"enabled": false, "driver": "none"}, nil
	}

	const key = "fxkit:cache-demo"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	val, err := s.cache.Get(ctx, key)
	switch {
	case err == nil:
		return map[string]any{"enabled": true, "hit": true, "value": string(val), "ttl": "30s"}, nil
	case errors.Is(err, cachex.ErrCacheMiss):
		if err := s.cache.Set(ctx, key, []byte(now), 30*time.Second); err != nil {
			if errors.Is(err, cachex.ErrCacheDisabled) {
				return map[string]any{"enabled": false, "driver": "none"}, nil
			}
			return nil, fxerrors.Wrap(err)
		}
		return map[string]any{"enabled": true, "hit": false, "value": now, "ttl": "30s"}, nil
	case errors.Is(err, cachex.ErrCacheDisabled):
		return map[string]any{"enabled": false, "driver": "none"}, nil
	default:
		return nil, fxerrors.Wrap(err)
	}
}

// getHeartbeat 读取 reminder 的累计 tick。
func (s *Service) getHeartbeat(ctx context.Context) (HeartbeatOut, error) {
	row, err := s.heartbeats.Get(ctx, "showcase")
	if err != nil {
		return HeartbeatOut{}, err
	}
	return HeartbeatOut{Ticks: row.Ticks, LastTick: row.LastTick}, nil
}
