package authz

import (
	"context"
	"sort"
	"strings"

	"github.com/fitan/fxkit/fxerrors"
)

// APIPermission 是被保护 API 的目录视图。
//
// 它是**派生**数据：来自「路由表（HTTPRoute）+ Casbin 策略」，不落库。
//
// 早期这里有一张 authz_api_permission 表，启动时由 SyncRouteCatalog 写入、
// 管理端 CRUD 维护。问题在于同一份事实存了两遍：
//   - method/path 在 casbin_rule（真正决定授权）
//   - summary/description/tags 在 Huma spec（运行时本来就有）
//
// 于是需要「启动同步」、会在两次部署之间漂移，还多出 5 个写接口和一处 TOCTOU。
// 现在直接派生，三者天然一致。
type APIPermission struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Pattern     string   `json:"pattern"`
	OperationID string   `json:"operation_id"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	// Grants 是当前被授予该 (obj, act) 的角色/主体（来自 Casbin p 规则）。
	Grants []string `json:"grants"`
}

// ListRouteCatalogInput 对权限目录做分页/过滤。
type ListRouteCatalogInput struct {
	Start          int
	Limit          int
	Q              string
	ReplyWithCount bool
}

// ListRouteCatalogResult 是分页后的目录响应。
type ListRouteCatalogResult struct {
	Items []APIPermission
	Total *int64
	Start int
	Limit int
}

// ListRouteCatalogPage 从路由表 + Casbin 策略派生一页目录。
//
// routes 通常来自 di.GetGroup[HTTPRoute]（PreStart 时已完整）。
// Casbin 未启用时 Grants 为空，只返回路由与元数据。
func (e *Enforcer) ListRouteCatalogPage(ctx context.Context, routes []HTTPRoute, in ListRouteCatalogInput) (ListRouteCatalogResult, error) {
	if e != nil && e.Enabled() && e.db == nil {
		return ListRouteCatalogResult{}, fxerrors.Internal("auth permission catalog requires database")
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

	grants := map[string][]string{}
	if e != nil && e.Enabled() {
		policies, err := e.ListPolicies(ctx)
		if err != nil {
			return ListRouteCatalogResult{}, err
		}
		for _, p := range policies {
			key := strings.ToUpper(p.Act) + " " + p.Obj
			grants[key] = append(grants[key], p.Sub)
		}
		for k := range grants {
			sort.Strings(grants[k])
		}
	}

	q := strings.ToLower(strings.TrimSpace(in.Q))
	items := make([]APIPermission, 0, len(routes))
	for _, r := range routes {
		pattern := HumaPathToCasbin(r.Path)
		item := APIPermission{
			Method:      strings.ToUpper(strings.TrimSpace(r.Method)),
			Path:        strings.TrimSpace(r.Path),
			Pattern:     pattern,
			OperationID: strings.TrimSpace(r.OperationID),
			Summary:     strings.TrimSpace(r.Summary),
			Description: strings.TrimSpace(r.Description),
			Tags:        r.Tags,
		}
		item.Grants = grants[item.Method+" "+pattern]
		if q != "" && !catalogMatches(item, q) {
			continue
		}
		items = append(items, item)
	}

	sort.SliceStable(items, func(a, b int) bool {
		if items[a].Path != items[b].Path {
			return items[a].Path < items[b].Path
		}
		return items[a].Method < items[b].Method
	})

	var total *int64
	if in.ReplyWithCount {
		n := int64(len(items))
		total = &n
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	return ListRouteCatalogResult{Items: items[start:end], Total: total, Start: start, Limit: limit}, nil
}

func catalogMatches(p APIPermission, q string) bool {
	fields := []string{p.Method, p.Path, p.Pattern, p.OperationID, p.Summary, p.Description}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	for _, t := range p.Tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}
