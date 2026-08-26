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

// RoleRecord is admin-facing role metadata (separate from casbin_rule).
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

// CreateRoleInput creates or updates role metadata.
type CreateRoleInput struct {
	Name        string
	DisplayName string
	Description string
}

// RolePermission is one path+method grant for a role.
type RolePermission struct {
	Path   string `json:"path"`   // Huma `{id}` or Casbin `:id`
	Method string `json:"method"` // GET / POST / * …
}

// SetRolePermissionsInput replaces all p rules for a role.
type SetRolePermissionsInput struct {
	Role  string
	Items []RolePermission
}

// SubjectRolesInput sets/lists subject↔role bindings.
type SubjectRolesInput struct {
	Subject string
	Roles   []string
}

// AddSubjectRoleInput binds one role to a subject.
type AddSubjectRoleInput struct {
	Subject string
	Role    string
}

func (e *Enforcer) requireEnabled() error {
	if !e.Enabled() {
		return fxerrors.Internal("auth casbin not enabled")
	}
	return nil
}

// EnsureRole upserts role metadata (used by bootstrap + CreateRole).
func (e *Enforcer) EnsureRole(ctx context.Context, in CreateRoleInput) (*RoleRecord, error) {
	_ = ctx
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
	err := e.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"display_name", "description", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return nil, err
	}
	var out RoleRecord
	if err := e.db.First(&out, "name = ?", name).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateRole is an alias of EnsureRole for admin APIs.
func (e *Enforcer) CreateRole(ctx context.Context, in CreateRoleInput) (*RoleRecord, error) {
	return e.EnsureRole(ctx, in)
}

// ListRoles returns role metadata; also surfaces roles that only exist in Casbin.
func (e *Enforcer) ListRoles(ctx context.Context) ([]RoleRecord, error) {
	_ = ctx
	if err := e.requireEnabled(); err != nil {
		return nil, err
	}
	meta := map[string]RoleRecord{}
	if e.db != nil {
		var rows []RoleRecord
		if err := e.db.Order("name").Find(&rows).Error; err != nil {
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

// GetRole returns one role or not_found.
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

// DeleteRoleInput removes role metadata and all casbin p/g for that role.
type DeleteRoleInput struct {
	Role string
}

// DeleteRole removes a role and its policies/bindings.
func (e *Enforcer) DeleteRole(ctx context.Context, in DeleteRoleInput) error {
	_ = ctx
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
		if err := e.db.Delete(&RoleRecord{}, "name = ?", role).Error; err != nil {
			return err
		}
	}
	return nil
}

// ListRolePermissions returns p rules for a role as path+method.
func (e *Enforcer) ListRolePermissions(ctx context.Context, role string) ([]RolePermission, error) {
	_ = ctx
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
		if len(r) < 3 {
			continue
		}
		out = append(out, RolePermission{Path: r[1], Method: r[2]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// SetRolePermissions replaces all permissions for a role.
func (e *Enforcer) SetRolePermissions(ctx context.Context, in SetRolePermissionsInput) error {
	_ = ctx
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
	rules := make([][]string, 0, len(in.Items))
	for _, item := range in.Items {
		path := HumaPathToCasbin(strings.TrimSpace(item.Path))
		method := strings.ToUpper(strings.TrimSpace(item.Method))
		if path == "" || method == "" {
			continue
		}
		rules = append(rules, []string{role, path, method})
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if a, ok := e.e.GetAdapter().(*gormAdapter); ok && a != nil {
		if err := a.replacePoliciesForSub(role, rules); err != nil {
			return err
		}
		return e.e.LoadPolicy()
	}
	if _, err := e.e.RemoveFilteredPolicy(0, role); err != nil {
		return err
	}
	for _, rule := range rules {
		if _, err := e.e.AddPolicy(rule[0], rule[1], rule[2]); err != nil {
			if reloadErr := e.e.LoadPolicy(); reloadErr != nil {
				slog.Error("auth casbin reload after failed SetRolePermissions", "error", reloadErr)
			}
			return err
		}
	}
	return nil
}

// AddRolePermissionInput adds one permission to a role.
type AddRolePermissionInput struct {
	Role   string
	Path   string
	Method string
}

// AddRolePermission adds a single p rule.
func (e *Enforcer) AddRolePermission(ctx context.Context, in AddRolePermissionInput) error {
	_ = ctx
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
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.e.AddPolicy(role, path, method)
	return err
}

// RemoveRolePermission removes one p rule.
func (e *Enforcer) RemoveRolePermission(ctx context.Context, in AddRolePermissionInput) error {
	_ = ctx
	if err := e.requireEnabled(); err != nil {
		return err
	}
	role := strings.TrimSpace(in.Role)
	path := HumaPathToCasbin(strings.TrimSpace(in.Path))
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if role == "" || path == "" || method == "" {
		return fxerrors.BadRequest("role, path, method are required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.e.RemovePolicy(role, path, method)
	return err
}

// ListSubjectRoles returns roles bound to a subject (user id).
func (e *Enforcer) ListSubjectRoles(ctx context.Context, subject string) ([]string, error) {
	_ = ctx
	if err := e.requireEnabled(); err != nil {
		return nil, err
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil, fxerrors.BadRequest("subject is required")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	roles, err := e.e.GetRolesForUser(subject)
	if err != nil {
		return nil, err
	}
	sort.Strings(roles)
	return roles, nil
}

// SetSubjectRoles replaces all role bindings for a subject.
func (e *Enforcer) SetSubjectRoles(ctx context.Context, in SubjectRolesInput) error {
	_ = ctx
	if err := e.requireEnabled(); err != nil {
		return err
	}
	subject := strings.TrimSpace(in.Subject)
	if subject == "" {
		return fxerrors.BadRequest("subject is required")
	}
	seen := make(map[string]struct{}, len(in.Roles))
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
		rules = append(rules, []string{subject, role})
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if a, ok := e.e.GetAdapter().(*gormAdapter); ok && a != nil {
		if err := a.replaceGroupingsForUser(subject, rules); err != nil {
			return err
		}
		return e.e.LoadPolicy()
	}
	if _, err := e.e.DeleteRolesForUser(subject); err != nil {
		return err
	}
	for _, rule := range rules {
		if _, err := e.e.AddGroupingPolicy(rule[0], rule[1]); err != nil {
			return err
		}
	}
	return nil
}

// AddSubjectRole binds one role.
func (e *Enforcer) AddSubjectRole(ctx context.Context, in AddSubjectRoleInput) error {
	_ = ctx
	if err := e.requireEnabled(); err != nil {
		return err
	}
	subject := strings.TrimSpace(in.Subject)
	role := strings.TrimSpace(in.Role)
	if subject == "" || role == "" {
		return fxerrors.BadRequest("subject and role are required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.e.AddGroupingPolicy(subject, role)
	return err
}

// RemoveSubjectRole unbinds one role.
func (e *Enforcer) RemoveSubjectRole(ctx context.Context, in AddSubjectRoleInput) error {
	_ = ctx
	if err := e.requireEnabled(); err != nil {
		return err
	}
	subject := strings.TrimSpace(in.Subject)
	role := strings.TrimSpace(in.Role)
	if subject == "" || role == "" {
		return fxerrors.BadRequest("subject and role are required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.e.RemoveGroupingPolicy(subject, role)
	return err
}

// ListBindings returns all user→role grouping rules.
func (e *Enforcer) ListBindings(ctx context.Context) ([]RoleBinding, error) {
	_ = ctx
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
		if len(g) < 2 {
			continue
		}
		out = append(out, RoleBinding{User: g[0], Role: g[1]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].User == out[j].User {
			return out[i].Role < out[j].Role
		}
		return out[i].User < out[j].User
	})
	return out, nil
}

// ListPolicies returns all casbin p rules.
func (e *Enforcer) ListPolicies(ctx context.Context) ([]Policy, error) {
	_ = ctx
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
		if len(p) < 3 {
			continue
		}
		out = append(out, Policy{Sub: p[0], Obj: p[1], Act: p[2]})
	}
	return out, nil
}
