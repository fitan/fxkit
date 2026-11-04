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
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"gorm.io/gorm"
)

// defaultRBACModel 是经典 RBAC（sub, obj, act），带角色继承。
// Dom 预留给未来的多租户 matcher；v1 忽略它。
// keyMatch2 让 /users/:id 这类模式与请求路径匹配。
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

// EnforceInput 是授权请求。Dom 为预留字段（v1 未使用）。
type EnforceInput struct {
	Sub string
	Dom string // 预留给多租户 RBAC
	Obj string // 例如 "/users/1" 或 "/users"
	Act string // 例如 "GET"
}

// Policy 是一条 casbin p 规则：角色/用户可以对 Obj 执行 Act。
type Policy struct {
	Sub string // 角色或主体
	Obj string // 路径模式，例如 "/users/:id"
	Act string // HTTP 方法或 "*"
}

// RoleBinding 是一条 casbin g 规则：用户继承角色。
type RoleBinding struct {
	User string
	Role string
}

// Enforcer 是 fxkit 的 Casbin 门面。
type Enforcer struct {
	mu      sync.RWMutex
	e       *casbin.Enforcer
	db      *gorm.DB // 权限目录（内存执行器为 nil）
	enabled bool

	reloadMu     sync.Mutex
	reloadCancel context.CancelFunc
	reloadWG     sync.WaitGroup
}

type enforcerParams struct {
	Config *Config
	DB     *gormx.Client `optional:"true"`
	LC     *di.Lifecycle `optional:"true"`
}

// NewEnforcer 在 auth.casbin.enabled 时构建基于 Postgres 的 Casbin 执行器。
// Casbin 关闭时返回一个禁用状态的空执行器（绝不返回 nil）。
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
	autoMigrate := p.DB.AutoMigrate()
	adapter, err := newGormAdapter(p.DB.Pool(), table, autoMigrate)
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
	if autoMigrate {
		if err := ensurePermissionCatalog(p.DB.Pool()); err != nil {
			return nil, fmt.Errorf("auth permission catalog: %w", err)
		}
		if err := ensureRoleTable(p.DB.Pool()); err != nil {
			return nil, fmt.Errorf("auth role table: %w", err)
		}
	} else {
		slog.Debug("auth casbin: auto_migrate disabled; DDL is owned by your SQL platform")
	}
	out.e = e
	out.db = p.DB.Pool()
	out.enabled = true
	reloadEvery := ac.Casbin.ReloadInterval
	out.attachPolicyReloader(p.LC, reloadEvery)
	slog.Info("auth casbin enabled", "table", table, "reload_interval", reloadEvery)
	return out, nil
}

// NewMemoryEnforcer 构建供测试使用的内存版 Casbin 执行器。
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

// Enabled 报告 Casbin 授权是否生效。
func (e *Enforcer) Enabled() bool {
	return e != nil && e.enabled && e.e != nil
}

// ReloadPolicies 从适配器重新加载内存模型（用于感知其它副本的写入）。
func (e *Enforcer) ReloadPolicies() error {
	if !e.Enabled() {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.e.LoadPolicy()
}

func (e *Enforcer) attachPolicyReloader(lc *di.Lifecycle, interval time.Duration) {
	if e == nil || lc == nil || interval <= 0 || !e.Enabled() {
		return
	}
	lc.Append(di.Hook{
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

// Enforce 检查 Sub 是否可以对 Obj 执行 Act。
func (e *Enforcer) Enforce(ctx context.Context, in EnforceInput) (bool, error) {
	_ = ctx
	_ = in.Dom // 预留
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
	start := time.Now()
	allowed, err := e.e.Enforce(sub, obj, act)
	e.mu.RUnlock()
	recordEnforce(ctx, time.Since(start), allowed, err)
	return allowed, err
}

// AddPolicies 添加 p 规则（由 Casbin 保证幂等）。
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

// AddRoleBindings 添加 g 规则（用户 → 角色）。
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

// HumaPathToCasbin 把 Huma 的 `{id}` 占位符转换成 Casbin 的 `:id`，供 keyMatch2 策略使用。
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
