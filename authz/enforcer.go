package authz

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

// defaultRBACModel is classic RBAC (sub, obj, act) with role inheritance.
// Dom is reserved for a future multi-tenant matcher; v1 ignores it.
// keyMatch2 matches /users/:id style patterns against request paths.
const defaultRBACModel = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && keyMatch2(r.obj, p.obj) && (r.act == p.act || p.act == "*")
`

// EnforceInput is the authorization request. Dom is reserved (unused in v1).
type EnforceInput struct {
	Sub string
	Dom string // reserved for multi-tenant RBAC
	Obj string // e.g. "/users/1" or "/users"
	Act string // e.g. "GET"
}

// Policy is a casbin p rule: role/user may Act on Obj.
type Policy struct {
	Sub string // role or subject
	Obj string // path pattern, e.g. "/users/:id"
	Act string // HTTP method or "*"
}

// RoleBinding is a casbin g rule: user inherits role.
type RoleBinding struct {
	User string
	Role string
}

// Enforcer is the fxkit Casbin facade.
type Enforcer struct {
	mu      sync.RWMutex
	e       *casbin.Enforcer
	db      *gorm.DB // permission catalog (nil for memory enforcer)
	enabled bool

	reloadMu     sync.Mutex
	reloadCancel context.CancelFunc
	reloadWG     sync.WaitGroup
}

type enforcerParams struct {
	fx.In
	Config *Config
	DB     *gormx.Client `optional:"true"`
	LC     fx.Lifecycle  `optional:"true"`
}

// NewEnforcer builds a Postgres-backed Casbin enforcer when auth.casbin.enabled.
// Returns a disabled no-op enforcer when Casbin is off (never nil).
func NewEnforcer(p enforcerParams) (*Enforcer, error) {
	out := &Enforcer{}
	if p.Config == nil || !p.Config.Enabled || !p.Config.Casbin.Enabled {
		slog.Info("auth casbin disabled")
		return out, nil
	}
	ac := p.Config
	if p.DB == nil || p.DB.Pool() == nil {
		return nil, fxerrors.Internal("auth.casbin.enabled requires gormx database")
	}
	table := strings.TrimSpace(ac.Casbin.TableName)
	adapter, err := newGormAdapter(p.DB.Pool(), table)
	if err != nil {
		return nil, fmt.Errorf("auth casbin adapter: %w", err)
	}
	m, err := model.NewModelFromString(defaultRBACModel)
	if err != nil {
		return nil, fmt.Errorf("auth casbin model: %w", err)
	}
	e, err := casbin.NewEnforcer(m, adapter)
	if err != nil {
		return nil, fmt.Errorf("auth casbin enforcer: %w", err)
	}
	e.EnableAutoSave(true)
	if err := e.LoadPolicy(); err != nil {
		return nil, fmt.Errorf("auth casbin load: %w", err)
	}
	if err := ensurePermissionCatalog(p.DB.Pool()); err != nil {
		return nil, fmt.Errorf("auth permission catalog: %w", err)
	}
	if err := ensureRoleTable(p.DB.Pool()); err != nil {
		return nil, fmt.Errorf("auth role table: %w", err)
	}
	out.e = e
	out.db = p.DB.Pool()
	out.enabled = true
	reloadEvery := ac.Casbin.ReloadInterval
	out.attachPolicyReloader(p.LC, reloadEvery)
	slog.Info("auth casbin enabled", "table", table, "reload_interval", reloadEvery)
	return out, nil
}

// NewMemoryEnforcer builds an in-memory Casbin enforcer for tests.
func NewMemoryEnforcer() (*Enforcer, error) {
	m, err := model.NewModelFromString(defaultRBACModel)
	if err != nil {
		return nil, err
	}
	e, err := casbin.NewEnforcer(m)
	if err != nil {
		return nil, err
	}
	return &Enforcer{e: e, enabled: true}, nil
}

// Enabled reports whether Casbin authorization is active.
func (e *Enforcer) Enabled() bool {
	return e != nil && e.enabled && e.e != nil
}

// ReloadPolicies replaces the in-memory model from the adapter (other replicas' writes).
func (e *Enforcer) ReloadPolicies() error {
	if !e.Enabled() {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.e.LoadPolicy()
}

func (e *Enforcer) attachPolicyReloader(lc fx.Lifecycle, interval time.Duration) {
	if e == nil || lc == nil || interval <= 0 || !e.Enabled() {
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, cancel := context.WithCancel(context.Background())
			e.reloadMu.Lock()
			e.reloadCancel = cancel
			e.reloadMu.Unlock()
			e.reloadWG.Add(1)
			go func() {
				defer e.reloadWG.Done()
				e.reloadLoop(ctx, interval)
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			e.reloadMu.Lock()
			cancel := e.reloadCancel
			e.reloadMu.Unlock()
			if cancel != nil {
				cancel()
			}
			e.reloadWG.Wait()
			return nil
		},
	})
}

func (e *Enforcer) reloadLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.ReloadPolicies(); err != nil {
				slog.Warn("auth casbin reload policy", "error", err)
			}
		}
	}
}

// Enforce checks whether Sub may Act on Obj.
func (e *Enforcer) Enforce(ctx context.Context, in EnforceInput) (bool, error) {
	_ = ctx
	_ = in.Dom // reserved
	if !e.Enabled() {
		return true, nil
	}
	sub := strings.TrimSpace(in.Sub)
	obj := normalizeRequestPath(in.Obj)
	act := strings.ToUpper(strings.TrimSpace(in.Act))
	if sub == "" || obj == "" || act == "" {
		return false, nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.e.Enforce(sub, obj, act)
}

// AddPolicies adds p rules (idempotent via Casbin).
func (e *Enforcer) AddPolicies(policies []Policy) error {
	if !e.Enabled() {
		return fxerrors.Internal("auth casbin not enabled")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range policies {
		sub, obj, act := strings.TrimSpace(p.Sub), strings.TrimSpace(p.Obj), strings.ToUpper(strings.TrimSpace(p.Act))
		if sub == "" || obj == "" || act == "" {
			continue
		}
		obj = HumaPathToCasbin(obj)
		if _, err := e.e.AddPolicy(sub, obj, act); err != nil {
			return err
		}
	}
	return nil
}

// AddRoleBindings adds g rules (user → role).
func (e *Enforcer) AddRoleBindings(bindings []RoleBinding) error {
	if !e.Enabled() {
		return fxerrors.Internal("auth casbin not enabled")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, b := range bindings {
		user, role := strings.TrimSpace(b.User), strings.TrimSpace(b.Role)
		if user == "" || role == "" {
			continue
		}
		if _, err := e.e.AddGroupingPolicy(user, role); err != nil {
			return err
		}
	}
	return nil
}

// HumaPathToCasbin converts Huma `{id}` placeholders to Casbin `:id` for keyMatch2 policies.
func HumaPathToCasbin(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	var b strings.Builder
	for i := 0; i < len(path); {
		if path[i] == '{' {
			j := strings.IndexByte(path[i:], '}')
			if j < 0 {
				b.WriteByte(path[i])
				i++
				continue
			}
			name := path[i+1 : i+j]
			b.WriteByte(':')
			b.WriteString(name)
			i += j + 1
			continue
		}
		b.WriteByte(path[i])
		i++
	}
	return b.String()
}
