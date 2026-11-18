package authz

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RoleRecord 是面向管理端的角色元数据（与 casbin_rule 分开存放）。
type RoleRecord struct {
	Name        string    `gorm:"primaryKey;size:64" json:"name"`
	DisplayName string    `gorm:"size:128" json:"display_name"`
	Description string    `gorm:"size:512" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (RoleRecord) TableName() string { return "authz_role" }

func ensureRoleTable(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	return db.AutoMigrate(&RoleRecord{})
}

// CreateRoleInput 创建或更新角色元数据。
type CreateRoleInput struct {
	Name        string
	DisplayName string
	Description string
}

// RolePermission 是授予角色的单条 path+method 权限。
type RolePermission struct {
	Domain string `json:"domain"` // 租户 domain；"*" = 全局
	Path   string `json:"path"`   // Huma 的 `{id}` 或 Casbin 的 `:id`
	Method string `json:"method"` // GET / POST / * …
}

// SetRolePermissionsInput 全量替换某个角色在某个 domain 的 p 规则。
type SetRolePermissionsInput struct {
	Role   string
	Domain string // 空 = "*"（全局）
	Items  []RolePermission
}

// SubjectRolesInput 设置/查询主体在某个 domain 的角色绑定。
type SubjectRolesInput struct {
	Subject string
	Domain  string // 空 = "*"
	Roles   []string
}

// AddSubjectRoleInput 把一个角色绑定到主体（可按 domain）。
type AddSubjectRoleInput struct {
	Subject string
	Role    string
	Domain  string // 空 = "*"
}

func (e *Enforcer) requireEnabled() error {
	if !e.Enabled() {
		return fxerrors.Internal("auth casbin not enabled")
	}
	return nil
}

// AutoBindOnce 在主体于指定 domain 尚无任何角色时绑定 bootstrap 角色。
// domain 为空时不做任何事（缺失租户不能获得全局角色）。
//
// 每个 (subject, domain) 只尝试一次；失败会撤销缓存，下次请求重试。
func (e *Enforcer) AutoBindOnce(ctx context.Context, subject, role, domain string) error {
	subject = strings.TrimSpace(subject)
	role = strings.TrimSpace(role)
	domain = strings.TrimSpace(domain)
	if subject == "" || role == "" || domain == "" || e == nil || !e.Enabled() {
		return nil
	}
	key := subject + "\x00" + domain
	e.bindMu.Lock()
	if e.bindSeen == nil {
		e.bindSeen = map[string]struct{}{}
	}
	if _, seen := e.bindSeen[key]; seen {
		e.bindMu.Unlock()
		return nil
	}
	if len(e.bindSeen) >= 4096 {
		e.bindSeen = map[string]struct{}{}
	}
	e.bindSeen[key] = struct{}{}
	e.bindMu.Unlock()

	err := func() error {
		roles, err := e.ListSubjectRolesInDomain(ctx, subject, domain)
		if err != nil {
			slog.Warn("authz auto-bind list roles", "subject", subject, "domain", domain, "error", err)
			return err
		}
		if len(roles) > 0 {
			return nil
		}
		if err := e.AddSubjectRole(ctx, AddSubjectRoleInput{Subject: subject, Role: role, Domain: domain}); err != nil {
			slog.Warn("authz auto-bind bootstrap role", "subject", subject, "role", role, "domain", domain, "error", err)
			return err
		}
		slog.Info("authz auto-bound bootstrap role", "subject", subject, "role", role, "domain", domain)
		return nil
	}()
	if err != nil {
		e.bindMu.Lock()
		delete(e.bindSeen, key)
		e.bindMu.Unlock()
	}
	return err
}

// EnsureRole 以 upsert 方式写入角色元数据（引导流程与 CreateRole 都会用到）。
func (e *Enforcer) EnsureRole(ctx context.Context, in CreateRoleInput) (*RoleRecord, error) {
	if err := e.requireEnabled(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fxerrors.BadRequest("role name is required")
	}
	if e.db == nil {
		return &RoleRecord{Name: name, DisplayName: in.DisplayName, Description: in.Description}, nil
	}
	now := time.Now().UTC()
	row := RoleRecord{
		Name:        name,
		DisplayName: strings.TrimSpace(in.DisplayName),
		Description: strings.TrimSpace(in.Description),
		UpdatedAt:   now,
		CreatedAt:   now,
	}
	if row.DisplayName == "" {
		row.DisplayName = name
	}
	err := e.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"display_name", "description", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return nil, err
	}
	var out RoleRecord
	if err := e.db.WithContext(ctx).First(&out, "name = ?", name).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateRole 是 EnsureRole 面向管理 API 的别名。
func (e *Enforcer) CreateRole(ctx context.Context, in CreateRoleInput) (*RoleRecord, error) {
	return e.EnsureRole(ctx, in)
}

// ListRoles 返回角色元数据；仅存在于 Casbin 中的角色也会一并列出。
func (e *Enforcer) ListRoles(ctx context.Context) ([]RoleRecord, error) {
	if err := e.requireEnabled(); err != nil {
		return nil, err
	}
	meta := map[string]RoleRecord{}
	if e.db != nil {
		var rows []RoleRecord
		if err := e.db.WithContext(ctx).Order("name").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			meta[r.Name] = r
		}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	policies, err := e.e.GetPolicy()
	if err != nil {
		return nil, err
	}
	for _, p := range policies {
		if len(p) < 1 {
			continue
		}
		name := strings.TrimSpace(p[0])
		if name == "" {
			continue
		}
		if _, ok := meta[name]; !ok {
			meta[name] = RoleRecord{Name: name, DisplayName: name}
		}
	}
	groupings, err := e.e.GetGroupingPolicy()
	if err != nil {
		return nil, err
	}
	for _, g := range groupings {
		if len(g) < 2 {
			continue
		}
		name := strings.TrimSpace(g[1])
		if name == "" {
			continue
		}
		if _, ok := meta[name]; !ok {
			meta[name] = RoleRecord{Name: name, DisplayName: name}
		}
	}
	out := make([]RoleRecord, 0, len(meta))
	for _, r := range meta {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetRole 返回单个角色，不存在时返回 not_found。
func (e *Enforcer) GetRole(ctx context.Context, name string) (*RoleRecord, error) {
	roles, err := e.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	for i := range roles {
		if roles[i].Name == name {
			return &roles[i], nil
		}
	}
	return nil, fxerrors.NotFound("role", "name=%s", name)
}

// DeleteRoleInput 删除角色元数据以及该角色的全部 casbin p/g 规则。
type DeleteRoleInput struct {
	Role string
}

// DeleteRole 删除角色及其策略/绑定。
func (e *Enforcer) DeleteRole(ctx context.Context, in DeleteRoleInput) error {
	if err := e.requireEnabled(); err != nil {
		return err
	}
	role := strings.TrimSpace(in.Role)
	if role == "" {
		return fxerrors.BadRequest("role is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.e.RemoveFilteredPolicy(0, role); err != nil {
		return err
	}
	if _, err := e.e.RemoveFilteredGroupingPolicy(1, role); err != nil {
		return err
	}
	if e.db != nil {
		if err := e.db.WithContext(ctx).Delete(&RoleRecord{}, "name = ?", role).Error; err != nil {
			return err
		}
	}
	return nil
}

// ListRolePermissions 以 path+method 形式返回角色的 p 规则。
func (e *Enforcer) ListRolePermissions(ctx context.Context, role string) ([]RolePermission, error) {
	if err := e.requireEnabled(); err != nil {
		return nil, err
	}
	role = strings.TrimSpace(role)
	if role == "" {
		return nil, fxerrors.BadRequest("role is required")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	rules, err := e.e.GetFilteredPolicy(0, role)
	if err != nil {
		return nil, err
	}
	out := make([]RolePermission, 0, len(rules))
	for _, r := range rules {
		if len(r) < 4 {
			continue
		}
		out = append(out, RolePermission{Domain: r[1], Path: r[2], Method: r[3]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		if out[i].Path == out[j].Path {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// SetRolePermissions 替换角色的全部权限。
func (e *Enforcer) SetRolePermissions(ctx context.Context, in SetRolePermissionsInput) error {
	if err := e.requireEnabled(); err != nil {
		return err
	}
	role := strings.TrimSpace(in.Role)
	if role == "" {
		return fxerrors.BadRequest("role is required")
	}
	if _, err := e.EnsureRole(ctx, CreateRoleInput{Name: role}); err != nil {
		return err
	}
	dom := normalizeDomain(in.Domain)
	rules := make([][]string, 0, len(in.Items))
	for _, item := range in.Items {
		path := HumaPathToCasbin(strings.TrimSpace(item.Path))
		method := strings.ToUpper(strings.TrimSpace(item.Method))
		if path == "" || method == "" {
			continue
		}
		rules = append(rules, []string{role, dom, path, method})
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if a, ok := e.e.GetAdapter().(*gormAdapter); ok && a != nil {
		if err := a.replacePoliciesForSubDomain(role, dom, rules); err != nil {
			return err
		}
		return e.e.LoadPolicy()
	}
	if _, err := e.e.RemoveFilteredPolicy(0, role, dom); err != nil {
		return err
	}
	for _, rule := range rules {
		if _, err := e.e.AddPolicy(rule[0], rule[1], rule[2], rule[3]); err != nil {
			if reloadErr := e.e.LoadPolicy(); reloadErr != nil {
				slog.Error("auth casbin reload after failed SetRolePermissions", "error", reloadErr)
			}
			return err
		}
	}
	return nil
}

// AddRolePermissionInput 为角色追加一条权限。
type AddRolePermissionInput struct {
	Role   string
	Domain string // 空 = "*"
	Path   string
	Method string
}

// AddRolePermission 添加单条 p 规则。
func (e *Enforcer) AddRolePermission(ctx context.Context, in AddRolePermissionInput) error {
	if err := e.requireEnabled(); err != nil {
		return err
	}
	role := strings.TrimSpace(in.Role)
	path := HumaPathToCasbin(strings.TrimSpace(in.Path))
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if role == "" || path == "" || method == "" {
		return fxerrors.BadRequest("role, path, method are required")
	}
	if _, err := e.EnsureRole(ctx, CreateRoleInput{Name: role}); err != nil {
		return err
	}
	dom := normalizeDomain(in.Domain)
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.e.AddPolicy(role, dom, path, method)
	return err
}

// RemoveRolePermission 删除单条 p 规则。
func (e *Enforcer) RemoveRolePermission(ctx context.Context, in AddRolePermissionInput) error {
	if err := e.requireEnabled(); err != nil {
		return err
	}
	role := strings.TrimSpace(in.Role)
	path := HumaPathToCasbin(strings.TrimSpace(in.Path))
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if role == "" || path == "" || method == "" {
		return fxerrors.BadRequest("role, path, method are required")
	}
	dom := normalizeDomain(in.Domain)
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.e.RemovePolicy(role, dom, path, method)
	return err
}

// ListSubjectRoles 返回主体在全局 domain（"*"）的角色。
func (e *Enforcer) ListSubjectRoles(ctx context.Context, subject string) ([]string, error) {
	return e.ListSubjectRolesInDomain(ctx, subject, DefaultDomain)
}

// ListSubjectRolesInDomain 返回主体在指定 domain 的角色。
func (e *Enforcer) ListSubjectRolesInDomain(ctx context.Context, subject, domain string) ([]string, error) {
	if err := e.requireEnabled(); err != nil {
		return nil, err
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil, fxerrors.BadRequest("subject is required")
	}
	domain = normalizeDomain(domain)
	e.mu.RLock()
	defer e.mu.RUnlock()
	roles, err := e.e.GetRolesForUser(subject, domain)
	if err != nil {
		return nil, err
	}
	sort.Strings(roles)
	return roles, nil
}

// SetSubjectRoles 替换主体的全部角色绑定。
func (e *Enforcer) SetSubjectRoles(ctx context.Context, in SubjectRolesInput) error {
	if err := e.requireEnabled(); err != nil {
		return err
	}
	subject := strings.TrimSpace(in.Subject)
	if subject == "" {
		return fxerrors.BadRequest("subject is required")
	}
	seen := make(map[string]struct{}, len(in.Roles))
	dom := normalizeDomain(in.Domain)
	rules := make([][]string, 0, len(in.Roles))
	for _, role := range in.Roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		rules = append(rules, []string{subject, role, dom})
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if a, ok := e.e.GetAdapter().(*gormAdapter); ok && a != nil {
		if err := a.replaceGroupingsForUserDomain(subject, dom, rules); err != nil {
			return err
		}
		return e.e.LoadPolicy()
	}
	if _, err := e.e.DeleteRolesForUserInDomain(subject, dom); err != nil {
		return err
	}
	for _, rule := range rules {
		if _, err := e.e.AddGroupingPolicy(rule[0], rule[1], rule[2]); err != nil {
			return err
		}
	}
	return nil
}

// AddSubjectRole 绑定一个角色。
func (e *Enforcer) AddSubjectRole(ctx context.Context, in AddSubjectRoleInput) error {
	if err := e.requireEnabled(); err != nil {
		return err
	}
	subject := strings.TrimSpace(in.Subject)
	role := strings.TrimSpace(in.Role)
	if subject == "" || role == "" {
		return fxerrors.BadRequest("subject and role are required")
	}
	dom := normalizeDomain(in.Domain)
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.e.AddGroupingPolicy(subject, role, dom)
	return err
}

// RemoveSubjectRole 解绑一个角色。
func (e *Enforcer) RemoveSubjectRole(ctx context.Context, in AddSubjectRoleInput) error {
	if err := e.requireEnabled(); err != nil {
		return err
	}
	subject := strings.TrimSpace(in.Subject)
	role := strings.TrimSpace(in.Role)
	if subject == "" || role == "" {
		return fxerrors.BadRequest("subject and role are required")
	}
	dom := normalizeDomain(in.Domain)
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.e.RemoveGroupingPolicy(subject, role, dom)
	return err
}

// ListBindings 返回全部用户→角色的 grouping 规则。
func (e *Enforcer) ListBindings(ctx context.Context) ([]RoleBinding, error) {
	if err := e.requireEnabled(); err != nil {
		return nil, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	gs, err := e.e.GetGroupingPolicy()
	if err != nil {
		return nil, err
	}
	out := make([]RoleBinding, 0, len(gs))
	for _, g := range gs {
		if len(g) < 3 {
			continue
		}
		out = append(out, RoleBinding{User: g[0], Role: g[1], Dom: g[2]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].User != out[j].User {
			return out[i].User < out[j].User
		}
		if out[i].Dom != out[j].Dom {
			return out[i].Dom < out[j].Dom
		}
		return out[i].Role < out[j].Role
	})
	return out, nil
}

// ListPolicies 返回全部 casbin p 规则。
func (e *Enforcer) ListPolicies(ctx context.Context) ([]Policy, error) {
	if err := e.requireEnabled(); err != nil {
		return nil, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	ps, err := e.e.GetPolicy()
	if err != nil {
		return nil, err
	}
	out := make([]Policy, 0, len(ps))
	for _, p := range ps {
		if len(p) < 4 {
			continue
		}
		out = append(out, Policy{Sub: p[0], Dom: p[1], Obj: p[2], Act: p[3]})
	}
	return out, nil
}
