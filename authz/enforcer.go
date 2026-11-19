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

// DefaultDomain 是全局（跨租户）策略/角色绑定的 domain。
const DefaultDomain = "*"

// normalizeDomain 去除首尾空白，空值回退为 [DefaultDomain]。
func normalizeDomain(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return DefaultDomain
	}
	return d
}

// defaultRBACModel 是带 domain 的 RBAC（sub, dom, obj, act），带角色继承。
//
// 匹配规则：
//   - policy 的 dom 等于请求 dom，或为 "*"（全局策略）；
//   - 角色绑定在请求 dom 或 "*"（跨租户管理员）；
//   - keyMatch2 让 /users/:id 这类模式与请求路径匹配。
const defaultRBACModel = `
[request_definition]
r = sub, dom, obj, act

[policy_definition]
p = sub, dom, obj, act

[role_definition]
g = _, _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = (r.dom == p.dom || p.dom == "*") && (g(r.sub, p.sub, r.dom) || g(r.sub, p.sub, "*")) && keyMatch2(r.obj, p.obj) && (r.act == p.act || p.act == "*")
`

// EnforceInput 是授权请求。
//
// Dom 是租户 domain（来自 JWT org claim）。**空 Dom 一律拒绝**：
// 缺失租户信息的 token 不能被当成全局身份。
// 跨租户管理员通过 Dom="*" 的 g/p 规则实现。
type EnforceInput struct {
	// Sub 主体标识（用户 ID 或角色），空 = 拒绝。
	Sub string
	// Dom 是租户 domain，通常取自 JWT 的 org claim；空一律拒绝。
	Dom string // 租户 domain；空 = 拒绝
	// Obj 是请求路径，由中间件传入规范化后的路径。
	Obj string // 例如 "/users/1" 或 "/users"
	// Act 是 HTTP 方法，比较前统一转大写。
	Act string // 例如 "GET"
}

// Policy 是一条 casbin p 规则：角色/用户可以在 Dom 下对 Obj 执行 Act。
type Policy struct {
	// Sub 角色或主体（json: sub）。
	Sub string `json:"sub"` // 角色或主体
	// Dom 租户 domain（json: dom）；"*" 表示全局。
	Dom string `json:"dom"` // 租户 domain；"*" = 全局
	// Obj 路径模式（json: obj），例如 "/users/:id"。
	Obj string `json:"obj"` // 路径模式，例如 "/users/:id"
	// Act HTTP 方法或 "*"（json: act）。
	Act string `json:"act"` // HTTP 方法或 "*"
}

// RoleBinding 是一条 casbin g 规则：用户在某 domain 下继承角色。
type RoleBinding struct {
	// User 主体标识（g 规则第一列）。
	User string `json:"user"`
	// Role 主体继承的角色名。
	Role string `json:"role"`
	// Dom 租户 domain；"*" = 全局角色
	Dom string `json:"dom"` // 租户 domain；"*" = 全局角色
}

// Enforcer 是 fxkit 的 Casbin 门面。
type Enforcer struct {
	// mu 保护底层 Casbin 执行器：判定持读锁，策略变更持写锁。
	mu sync.RWMutex
	// e 是底层 Casbin 执行器；未启用时为 nil。
	e *casbin.Enforcer
	// db 是角色元数据与权限目录所用的数据库；内存执行器为 nil。
	db *gorm.DB // 权限目录（内存执行器为 nil）
	// enabled 标记 Casbin 授权是否生效。
	enabled bool

	// auto-bind 缓存（Huma / mux 中间件共用）。
	// bindMu 保护 bindSeen。
	bindMu sync.Mutex
	// bindSeen 记录已尝试 auto-bind 的 subject+domain，达到 4096 条后整体清空。
	bindSeen map[string]struct{}

	// reloadMu 保护 reloadCancel。
	reloadMu sync.Mutex
	// reloadCancel 取消后台策略重载循环；未启动时为 nil。
	reloadCancel context.CancelFunc
	// reloadWG 等待后台策略重载循环退出。
	reloadWG sync.WaitGroup
}

// enforcerParams 是 [NewEnforcer] 的 Fx 入参。
type enforcerParams struct {
	// Config 是 auth 配置，决定 Casbin 是否启用。
	Config *Config
	// DB 是可选数据库客户端；Casbin 关闭时可为 nil。
	DB *gormx.Client `optional:"true"`
	// LC 是可选的钩子生命周期，用于挂载策略重载与关闭 hook。
	LC *di.Lifecycle `optional:"true"`
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
	// casbin.NewEnforcer 在 adapter 非 nil 时会自动 LoadPolicy；
	// 再调一次只是重复读一遍策略表。
	e.EnableAutoSave(true)
	if autoMigrate {
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
	err := e.e.LoadPolicy()
	recordPolicyReload(context.Background(), err)
	return err
}

// attachPolicyReloader 向生命周期注册定时重载策略的 hook。
// 执行器未启用、lc 为 nil 或 interval<=0 时不注册。
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

// reloadLoop 每隔 interval 调用 [Enforcer.ReloadPolicies]，ctx 取消后退出。
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
	if !e.Enabled() {
		return true, nil
	}
	sub := strings.TrimSpace(in.Sub)
	dom := strings.TrimSpace(in.Dom)
	obj := normalizeRequestPath(in.Obj)
	act := strings.ToUpper(strings.TrimSpace(in.Act))
	if sub == "" || dom == "" || obj == "" || act == "" {
		return false, nil
	}
	e.mu.RLock()
	start := time.Now()
	// 必须 defer：Casbin 的 keyMatch2 对非法 policy 路径会 panic，
	// 非 defer 的 RUnlock 会让读写锁永久泄漏，后续 ReloadPolicies 全部楔死。
	defer e.mu.RUnlock()
	allowed, err := e.e.Enforce(sub, dom, obj, act)
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
		dom := normalizeDomain(p.Dom)
		obj = HumaPathToCasbin(obj)
		if _, err := e.e.AddPolicy(sub, dom, obj, act); err != nil {
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
		dom := normalizeDomain(b.Dom)
		if _, err := e.e.AddGroupingPolicy(user, role, dom); err != nil {
			return err
		}
	}
	return nil
}

// HumaPathToCasbin 把 Huma 的 `{id}` 占位符转换成 Casbin 的 `:id`，供 keyMatch2 策略使用。
//
// 非占位符部分按字面量转义正则元字符。keyMatch2 会把 policy 当正则编译：
// `/files/{name}.json` 中的 `.` 会匹配任意字符（`xXjson` 也能通过），
// 而 `(` 这类字符会让 regexp 编译失败并 panic。转义后 `.` 只匹配字面点号，
// 非法字符也不再能制造 panic。`:`（参数）与 `*`（通配）保留语义。
func HumaPathToCasbin(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	var b strings.Builder
	for i := 0; i < len(path); {
		if path[i] == '{' {
			j := strings.IndexByte(path[i:], '}')
			if j > 0 {
				name := path[i+1 : i+j]
				b.WriteByte(':')
				b.WriteString(name)
				i += j + 1
				continue
			}
		}
		escapeCasbinLiteral(&b, path[i])
		i++
	}
	if b.Len() == 0 {
		return "/"
	}
	return b.String()
}

// escapeCasbinLiteral 写入一个字面量字节，必要时加反斜杠。
func escapeCasbinLiteral(b *strings.Builder, c byte) {
	switch c {
	case '.', '+', '(', ')', '[', ']', '?', '^', '$', '|', '\\', '{', '}':
		b.WriteByte('\\')
	}
	b.WriteByte(c)
}
