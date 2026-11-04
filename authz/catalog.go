package authz

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// APIPermission 是被保护 API 的目录行（Huma op 元数据 + path/method）。
// 与 casbin_rule 分开存放，便于管理端 UI 展示「这条权限是什么」。
type APIPermission struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Method      string    `gorm:"size:16;uniqueIndex:uk_authz_api;not null" json:"method"`
	Path        string    `gorm:"size:255;uniqueIndex:uk_authz_api;not null" json:"path"` // Huma 的 `{id}` 形式
	Pattern     string    `gorm:"size:255" json:"pattern"`                                // Casbin 的 `:id` 形式
	OperationID string    `gorm:"size:128" json:"operation_id"`
	Summary     string    `gorm:"size:255" json:"summary"`
	Description string    `gorm:"size:1024" json:"description"`
	Tags        string    `gorm:"size:255" json:"tags"` // 逗号分隔
	UpdatedAt   time.Time `json:"updated_at"`
}

func (APIPermission) TableName() string { return "authz_api_permission" }

func ensurePermissionCatalog(db *gorm.DB) error {
	return db.AutoMigrate(&APIPermission{})
}

// SyncRouteCatalog 为每条路由 upsert Huma 元数据（按 method+path 幂等）。
func (e *Enforcer) SyncRouteCatalog(routes []HTTPRoute) error {
	if e == nil || e.db == nil || len(routes) == 0 {
		return nil
	}
	rows := make([]APIPermission, 0, len(routes))
	seen := map[string]struct{}{}
	now := time.Now().UTC()
	for _, r := range routes {
		method := strings.ToUpper(strings.TrimSpace(r.Method))
		path := strings.TrimSpace(r.Path)
		if method == "" || path == "" {
			continue
		}
		key := method + "\x00" + path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		rows = append(rows, APIPermission{
			Method:      method,
			Path:        path,
			Pattern:     HumaPathToCasbin(path),
			OperationID: strings.TrimSpace(r.OperationID),
			Summary:     strings.TrimSpace(r.Summary),
			Description: strings.TrimSpace(r.Description),
			Tags:        strings.Join(r.Tags, ","),
			UpdatedAt:   now,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return e.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "method"}, {Name: "path"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"pattern", "operation_id", "summary", "description", "tags", "updated_at",
		}),
	}).Create(&rows).Error
}

// ListRouteCatalog 返回全部已登记的 API 权限（供管理端 / 排查使用）。
func (e *Enforcer) ListRouteCatalog() ([]APIPermission, error) {
	if e == nil || e.db == nil {
		return nil, nil
	}
	var rows []APIPermission
	if err := e.db.Order("path, method").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListRouteCatalogInput 对权限目录做分页/过滤。
type ListRouteCatalogInput struct {
	Start          int
	Limit          int
	Q              string // 在 method/path/pattern/operation_id/summary/tags 上做子串匹配
	ReplyWithCount bool
}

// ListRouteCatalogResult 是分页后的目录响应。
type ListRouteCatalogResult struct {
	Items []APIPermission
	Total *int64
	Start int
	Limit int
}

// ListRouteCatalogPage 返回一页目录行。
func (e *Enforcer) ListRouteCatalogPage(ctx context.Context, in ListRouteCatalogInput) (ListRouteCatalogResult, error) {
	_ = ctx
	if e == nil || e.db == nil {
		return ListRouteCatalogResult{Items: nil, Start: 0, Limit: 0}, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 1000 {
		limit = 1000
	}
	start := in.Start
	if start < 0 {
		start = 0
	}

	tx := e.db.Model(&APIPermission{})
	if q := strings.TrimSpace(in.Q); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		tx = tx.Where(
			"LOWER(method) LIKE ? OR LOWER(path) LIKE ? OR LOWER(pattern) LIKE ? OR LOWER(operation_id) LIKE ? OR LOWER(summary) LIKE ? OR LOWER(description) LIKE ? OR LOWER(tags) LIKE ?",
			like, like, like, like, like, like, like,
		)
	}

	var total *int64
	if in.ReplyWithCount {
		var n int64
		if err := tx.Count(&n).Error; err != nil {
			return ListRouteCatalogResult{}, err
		}
		total = &n
	}

	var rows []APIPermission
	if err := tx.Order("path, method").Offset(start).Limit(limit).Find(&rows).Error; err != nil {
		return ListRouteCatalogResult{}, err
	}
	return ListRouteCatalogResult{Items: rows, Total: total, Start: start, Limit: limit}, nil
}

// CreateAPIPermissionInput 创建一条目录行（管理端手工录入）。
type CreateAPIPermissionInput struct {
	Method      string
	Path        string
	OperationID string
	Summary     string
	Description string
	Tags        []string
}

// UpdateAPIPermissionInput 按 id 更新目录行（method/path 允许变更）。
type UpdateAPIPermissionInput struct {
	ID          uint
	Method      string
	Path        string
	OperationID string
	Summary     string
	Description string
	Tags        []string
}

// DeleteAPIPermissionInput 按 id 删除目录行。
type DeleteAPIPermissionInput struct {
	ID uint
}

func (e *Enforcer) requireCatalogDB() error {
	if e == nil || e.db == nil {
		return fxerrors.Internal("auth permission catalog requires database")
	}
	return nil
}

func normalizeCatalogFields(in CreateAPIPermissionInput) (APIPermission, error) {
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	path := strings.TrimSpace(in.Path)
	if method == "" || path == "" {
		return APIPermission{}, fxerrors.BadRequest("method and path are required")
	}
	return APIPermission{
		Method:      method,
		Path:        path,
		Pattern:     HumaPathToCasbin(path),
		OperationID: strings.TrimSpace(in.OperationID),
		Summary:     strings.TrimSpace(in.Summary),
		Description: strings.TrimSpace(in.Description),
		Tags:        strings.Join(normalizeTags(in.Tags), ","),
		UpdatedAt:   time.Now().UTC(),
	}, nil
}

func normalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// CreateAPIPermission 插入一条目录行。
func (e *Enforcer) CreateAPIPermission(ctx context.Context, in CreateAPIPermissionInput) (*APIPermission, error) {
	_ = ctx
	if err := e.requireCatalogDB(); err != nil {
		return nil, err
	}
	row, err := normalizeCatalogFields(in)
	if err != nil {
		return nil, err
	}
	var existing APIPermission
	err = e.db.Where("method = ? AND path = ?", row.Method, row.Path).First(&existing).Error
	if err == nil {
		return nil, fxerrors.Conflict("permission already exists (method=%s path=%s)", row.Method, row.Path)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err := e.db.Create(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// UpdateAPIPermission 按 id 更新目录行。
func (e *Enforcer) UpdateAPIPermission(ctx context.Context, in UpdateAPIPermissionInput) (*APIPermission, error) {
	_ = ctx
	if err := e.requireCatalogDB(); err != nil {
		return nil, err
	}
	if in.ID == 0 {
		return nil, fxerrors.BadRequest("id is required")
	}
	row, err := normalizeCatalogFields(CreateAPIPermissionInput{
		Method: in.Method, Path: in.Path, OperationID: in.OperationID,
		Summary: in.Summary, Description: in.Description, Tags: in.Tags,
	})
	if err != nil {
		return nil, err
	}
	var existing APIPermission
	if err := e.db.First(&existing, in.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fxerrors.NotFound("permission", "id=%d", in.ID)
		}
		return nil, err
	}
	var clash APIPermission
	err = e.db.Where("method = ? AND path = ? AND id <> ?", row.Method, row.Path, in.ID).First(&clash).Error
	if err == nil {
		return nil, fxerrors.Conflict("permission already exists (method=%s path=%s)", row.Method, row.Path)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	existing.Method = row.Method
	existing.Path = row.Path
	existing.Pattern = row.Pattern
	existing.OperationID = row.OperationID
	existing.Summary = row.Summary
	existing.Description = row.Description
	existing.Tags = row.Tags
	existing.UpdatedAt = row.UpdatedAt
	if err := e.db.Save(&existing).Error; err != nil {
		return nil, err
	}
	return &existing, nil
}

// DeleteAPIPermission 按 id 删除目录行（不触碰 Casbin 的 p 规则）。
func (e *Enforcer) DeleteAPIPermission(ctx context.Context, in DeleteAPIPermissionInput) error {
	_ = ctx
	if err := e.requireCatalogDB(); err != nil {
		return err
	}
	if in.ID == 0 {
		return fxerrors.BadRequest("id is required")
	}
	res := e.db.Delete(&APIPermission{}, in.ID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fxerrors.NotFound("permission", "id=%d", in.ID)
	}
	return nil
}

// GetAPIPermission 按 id 返回一条目录行。
func (e *Enforcer) GetAPIPermission(ctx context.Context, id uint) (*APIPermission, error) {
	_ = ctx
	if err := e.requireCatalogDB(); err != nil {
		return nil, err
	}
	if id == 0 {
		return nil, fxerrors.BadRequest("id is required")
	}
	var row APIPermission
	if err := e.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fxerrors.NotFound("permission", "id=%d", id)
		}
		return nil, err
	}
	return &row, nil
}

// HTTPRouteFromOperation 把 Huma operation 的身份字段复制到 [HTTPRoute]。
func HTTPRouteFromOperation(op huma.Operation) HTTPRoute {
	tags := append([]string(nil), op.Tags...)
	return HTTPRoute{
		Method:      op.Method,
		Path:        op.Path,
		OperationID: op.OperationID,
		Summary:     op.Summary,
		Description: op.Description,
		Tags:        tags,
	}
}

// HTTPRoutesFromOperations 把 Huma operation 转换为 authz 路由（跳过空 Path）。
func HTTPRoutesFromOperations(ops ...huma.Operation) []HTTPRoute {
	out := make([]HTTPRoute, 0, len(ops))
	for _, op := range ops {
		if strings.TrimSpace(op.Path) == "" {
			continue
		}
		out = append(out, HTTPRouteFromOperation(op))
	}
	return out
}

// HTTPRoutesFromOpenAPI 把当前生效的 Huma operation 复制成 HTTPRoute 行，
// 使未走 ProvideHTTPRoutes 的 RegisterResource / Register 也能获得 Casbin 策略。
func HTTPRoutesFromOpenAPI(api huma.API) []HTTPRoute {
	if api == nil {
		return nil
	}
	doc := api.OpenAPI()
	if doc == nil || doc.Paths == nil {
		return nil
	}
	var ops []huma.Operation
	for path, item := range doc.Paths {
		if item == nil {
			continue
		}
		add := func(method string, op *huma.Operation) {
			if op == nil {
				return
			}
			cp := *op
			if strings.TrimSpace(cp.Method) == "" {
				cp.Method = method
			}
			if strings.TrimSpace(cp.Path) == "" {
				cp.Path = path
			}
			if skipAuthzPath(cp.Path) || isPublicOperation(&cp) {
				return
			}
			ops = append(ops, cp)
		}
		add(http.MethodGet, item.Get)
		add(http.MethodPost, item.Post)
		add(http.MethodPut, item.Put)
		add(http.MethodPatch, item.Patch)
		add(http.MethodDelete, item.Delete)
		add(http.MethodHead, item.Head)
		add(http.MethodOptions, item.Options)
		add(http.MethodTrace, item.Trace)
	}
	return HTTPRoutesFromOperations(ops...)
}

func mergeHTTPRoutes(base, extra []HTTPRoute) []HTTPRoute {
	key := func(r HTTPRoute) string {
		return strings.ToUpper(strings.TrimSpace(r.Method)) + "\x00" + strings.TrimSpace(r.Path)
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]HTTPRoute, 0, len(base)+len(extra))
	for _, r := range base {
		k := key(r)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, r)
	}
	for _, r := range extra {
		k := key(r)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, r)
	}
	return out
}
