package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/server"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
)

type Config struct {
	ReminderPeriod string `yaml:"reminder_period"`
}

func (c *Config) SetDefaults() {
	c.ReminderPeriod = "15s"
}

type Counter struct {
	ActorID   string    `gorm:"primaryKey;size:64"`
	Value     int64     `gorm:"not null"`
	UpdatedAt time.Time `json:"-"`
}

func (Counter) TableName() string { return "catalog_counters" }

type Heartbeat struct {
	ActorID   string    `gorm:"primaryKey;size:64"`
	Ticks     int64     `gorm:"not null"`
	LastTick  time.Time `gorm:"not null"`
	UpdatedAt time.Time
}

func (Heartbeat) TableName() string { return "catalog_heartbeats" }

type IncInput struct {
	hatchetx.ActorRef
	Delta  int64 `json:"delta"`
	HoldMs int   `json:"holdMs,omitempty"`
}

type IncOutput struct {
	Value         int64 `json:"value"`
	Inflight      int32 `json:"inflight"`      // process-wide incCounter at handler entry
	ActorInflight int32 `json:"actorInflight"` // this actorId at handler entry
}

type HeartbeatIn struct {
	hatchetx.ActorRef
	ReminderName string `json:"reminderName"`
}

type HeartbeatOut struct {
	Ticks    int64     `json:"ticks"`
	LastTick time.Time `json:"lastTick"`
}

type Service struct {
	client    *gormx.Client
	hatchet   *hatchetx.Client
	enforcer  *authz.Enforcer
	cfg       *Config
	counter   *hatchetx.Actor[IncInput, IncOutput]
	heartbeat *hatchetx.Actor[HeartbeatIn, HeartbeatOut]
}

func NewService(client *gormx.Client, ht *hatchetx.Client, enf *authz.Enforcer, cfg *Config, lc fx.Lifecycle) (*Service, error) {
	if client == nil {
		return nil, fxerrors.Internal("platform: nil gormx client")
	}
	if cfg == nil {
		cfg = &Config{}
		cfg.SetDefaults()
	}
	s := &Service{client: client, hatchet: ht, enforcer: enf, cfg: cfg}
	s.counter = hatchetx.NewActor("catalog-counter", s.incCounter, hatchetx.WithActorDescription("article view counter"))
	s.heartbeat = hatchetx.NewActor("catalog-heartbeat", s.onHeartbeat, hatchetx.WithActorDescription("reminder tick"))
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := client.Conn(ctx).AutoMigrate(&Counter{}, &Heartbeat{}); err != nil {
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
	row := Counter{ActorID: id}
	if err := s.client.Conn(ctx).Where("actor_id = ?", id).FirstOrCreate(&row, Counter{ActorID: id}).Error; err != nil {
		return IncOutput{}, fxerrors.Wrap(err)
	}
	row.Value += delta
	if err := s.client.Conn(ctx).Model(&Counter{}).Where("actor_id = ?", id).Update("value", row.Value).Error; err != nil {
		return IncOutput{}, fxerrors.Wrap(err)
	}
	return IncOutput{Value: row.Value, Inflight: global, ActorInflight: actorN}, nil
}

func (s *Service) onHeartbeat(ctx context.Context, in HeartbeatIn) (HeartbeatOut, error) {
	id := strings.TrimSpace(in.ActorID)
	if id == "" {
		id = "showcase"
	}
	now := time.Now().UTC()
	row := Heartbeat{ActorID: id}
	if err := s.client.Conn(ctx).Where("actor_id = ?", id).
		Attrs(Heartbeat{ActorID: id, LastTick: now}).
		FirstOrCreate(&row).Error; err != nil {
		return HeartbeatOut{}, fxerrors.Wrap(err)
	}
	row.Ticks++
	row.LastTick = now
	if err := s.client.Conn(ctx).Save(&row).Error; err != nil {
		return HeartbeatOut{}, fxerrors.Wrap(err)
	}
	return HeartbeatOut{Ticks: row.Ticks, LastTick: row.LastTick}, nil
}

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

func (s *Service) maybeReminder(ctx context.Context) {
	if s.hatchet == nil || !s.hatchet.Enabled() {
		return
	}
	period := strings.TrimSpace(s.cfg.ReminderPeriod)
	if period == "" {
		period = "15s"
	}
	_, err := hatchetx.CreateReminder(ctx, s.hatchet, hatchetx.CreateReminderInput{
		WorkflowName: s.heartbeat.Name(),
		ActorID:      "showcase",
		ReminderName: "tick",
		Period:       period,
	})
	if err != nil {
		slog.Info("hatchetx reminder skipped", "error", err) // duplicate Name+Expression on replica restart
	}
}

func (s *Service) getCounter(ctx context.Context, id string) (IncOutput, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return IncOutput{}, fxerrors.Validation(map[string]string{"id": "required"})
	}
	var row Counter
	err := s.client.Conn(ctx).Where("actor_id = ?", id).First(&row).Error
	if err != nil {
		return IncOutput{Value: 0}, nil
	}
	return IncOutput{Value: row.Value}, nil
}

func (s *Service) getHeartbeat(ctx context.Context) (HeartbeatOut, error) {
	var row Heartbeat
	err := s.client.Conn(ctx).Where("actor_id = ?", "showcase").First(&row).Error
	if err != nil {
		return HeartbeatOut{}, nil
	}
	return HeartbeatOut{Ticks: row.Ticks, LastTick: row.LastTick}, nil
}

// --- Huma ---

type pingOut struct {
	Body map[string]any
}

type meOut struct {
	Body map[string]any
}

type counterPath struct {
	ID string `path:"id"`
}

type incIn struct {
	ID   string `path:"id"`
	Body struct {
		Delta  int64 `json:"delta"`
		HoldMs int   `json:"holdMs,omitempty"`
	}
}

type counterOut struct {
	Body IncOutput
}

type heartbeatOut struct {
	Body HeartbeatOut
}

var (
	pingOp = huma.Operation{
		OperationID: "publicPing",
		Method:      http.MethodGet,
		Path:        "/public/ping",
		Summary:     "Anonymous ping (authz.public)",
		Tags:        []string{"Platform"},
		Metadata:    map[string]any{authz.OpPublic: true},
	}
	meOp = huma.Operation{
		OperationID: "getMe",
		Method:      http.MethodGet,
		Path:        "/me",
		Summary:     "Current authz subject",
		Tags:        []string{"Platform"},
	}
	getCounterOp = huma.Operation{
		OperationID: "getCounter",
		Method:      http.MethodGet,
		Path:        "/counters/{id}",
		Summary:     "Read actor-backed counter (DB, no Hatchet call)",
		Tags:        []string{"Platform"},
	}
	incCounterOp = huma.Operation{
		OperationID: "incCounter",
		Method:      http.MethodPost,
		Path:        "/counters/{id}",
		Summary:     "Increment via hatchetx.NewActor",
		Tags:        []string{"Platform"},
	}
	heartbeatOp = huma.Operation{
		OperationID: "getHeartbeat",
		Method:      http.MethodGet,
		Path:        "/heartbeat",
		Summary:     "Last hatchetx reminder tick",
		Tags:        []string{"Platform"},
	}
)

func Register(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.Register(api, pingOp, func(context.Context, *struct{}) (*pingOut, error) {
			return &pingOut{Body: map[string]any{"ok": true, "public": true}}, nil
		})
		fxhuma.Register(api, meOp, func(ctx context.Context, _ *struct{}) (*meOut, error) {
			subj, ok := authz.SubjectFromContext(ctx)
			if !ok {
				return nil, fxerrors.Unauthorized("no subject")
			}
			return &meOut{Body: map[string]any{
				"id":    subj.ID,
				"orgId": subj.OrgID,
			}}, nil
		})
		fxhuma.Register(api, getCounterOp, func(ctx context.Context, in *counterPath) (*counterOut, error) {
			out, err := svc.getCounter(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &counterOut{Body: out}, nil
		})
		fxhuma.Register(api, incCounterOp, func(ctx context.Context, in *incIn) (*counterOut, error) {
			if svc.hatchet == nil || !svc.hatchet.Enabled() {
				return nil, fxerrors.Unavailable("hatchet disabled")
			}
			out, err := svc.counter.Call(ctx, svc.hatchet, IncInput{
				ActorRef: hatchetx.ActorRef{ActorID: in.ID},
				Delta:    in.Body.Delta,
				HoldMs:   in.Body.HoldMs,
			})
			if err != nil {
				return nil, fxerrors.Unavailable("actor: %v", err)
			}
			return &counterOut{Body: out}, nil
		})
		fxhuma.Register(api, heartbeatOp, func(ctx context.Context, _ *struct{}) (*heartbeatOut, error) {
			out, err := svc.getHeartbeat(ctx)
			if err != nil {
				return nil, err
			}
			return &heartbeatOut{Body: out}, nil
		})
	})
}

func platformHTTPRoutes() []authz.HTTPRoute {
	return authz.HTTPRoutesFromOperations(pingOp, meOp, getCounterOp, incCounterOp, heartbeatOp)
}

func RegisterWorker(svc *Service) hatchetx.Registrar {
	return func(client *hatchetx.Client) ([]hatchetx.WorkflowBase, error) {
		a, err := svc.counter.Register(client)
		if err != nil {
			return nil, err
		}
		b, err := svc.heartbeat.Register(client)
		if err != nil {
			return nil, err
		}
		return append(a, b...), nil
	}
}

func chiHeader() server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Fxkit-Chi", "1")
			next.ServeHTTP(w, r)
		})
	}
}

func humaHeader() fxhuma.MiddlewareRegistrar {
	return fxhuma.MiddlewareFunc(func(_ huma.API) fxhuma.Middleware {
		return func(ctx huma.Context, next func(huma.Context)) {
			ctx.SetHeader("X-Fxkit-Huma", "1")
			next(ctx)
		}
	})
}

func chiRoutes() []server.Route {
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"echo": r.URL.Query().Get("msg"),
			"via":  "chi",
		})
	})
	errors := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := chi.URLParam(r, "kind")
		switch kind {
		case "notfound":
			fxerrors.WriteError(w, r, fxerrors.NotFound("demo", "missing"))
		case "conflict":
			fxerrors.WriteError(w, r, fxerrors.Conflict("already exists"))
		case "validation":
			fxerrors.WriteError(w, r, fxerrors.Validation(map[string]string{"field": "required"}))
		case "boom":
			fxerrors.WriteError(w, r, fxerrors.Internal("secret cause"))
		default:
			fxerrors.WriteError(w, r, fxerrors.BadRequest("unknown kind %s", kind))
		}
	})
	tree := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"path": r.URL.Path, "via": "PrefixRoute"})
	})
	return []server.Route{
		{Pattern: "/chi/echo", Methods: []string{http.MethodGet}, Handler: echo},
		{Pattern: "/chi/errors/{kind}", Methods: []string{http.MethodGet}, Handler: errors},
		server.PrefixRoute("/chi/tree/", tree),
	}
}

var Module = fx.Options(
	fxkit.Service("platform", NewService,
		fxkit.ProvideConfig[Config]("catalog"),
		fxkit.Routes(chiRoutes),
	),
	server.ProvideMiddleware(chiHeader),
	fxhuma.ProvideMiddleware(humaHeader),
	fxhuma.ProvideRegistrar(Register),
	hatchetx.ProvideRegistrar(RegisterWorker),
	authz.ProvideHTTPRoutes(platformHTTPRoutes),
)
