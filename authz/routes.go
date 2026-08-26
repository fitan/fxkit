package authz

import (
	"net/http"
	"strings"

	"go.uber.org/fx"
)

// HTTPRoute 登记需要鉴权的 Huma/REST 路由，并携带 OpenAPI 元数据便于权限目录展示。
//
// Path 与 Huma operation 路径模板一致，支持 `{id}` 占位；Method 为空表示任意方法。
//
// Casbin 启用时：用请求 path + METHOD 做 Enforce（Permission 忽略）。
// Casbin 关闭时：Permission 非空则检查 Subject.Has；为空表示「仅需登录」。
//
// 推荐用 [HTTPRouteFromOperation] / [HTTPRoutesFromOperations] 从 huma.Operation 生成，
// 这样 OperationID / Summary / Tags 会写入 authz_api_permission 目录表。
//
//	authz.HTTPRoutesFromOperations(huma.Operation{
//	    OperationID: "listUsers", Method: "GET", Path: "/users", Summary: "列出用户",
//	})
type HTTPRoute struct {
	Method      string
	Path        string
	Permission  string   // legacy: only used when Casbin is disabled
	OperationID string   // Huma OperationID
	Summary     string   // Huma Summary（权限中文/说明）
	Description string   // Huma Description
	Tags        []string // Huma Tags
}

func (r HTTPRoute) match(method, path string) bool {
	if r.Method != "" && !strings.EqualFold(r.Method, method) {
		return false
	}
	return pathMatches(r.Path, path)
}

func pathMatches(pattern, path string) bool {
	pattern = normalizeRequestPath(pattern)
	path = normalizeRequestPath(path)
	pp := splitPath(pattern)
	sp := splitPath(path)
	if len(pp) != len(sp) {
		return false
	}
	for i := range pp {
		if strings.HasPrefix(pp[i], "{") && strings.HasSuffix(pp[i], "}") {
			continue
		}
		if pp[i] != sp[i] {
			return false
		}
	}
	return true
}

// normalizeRequestPath trims space and a trailing slash so findRoute and Casbin
// Enforce see the same path (GET /users/ ≡ /users).
func normalizeRequestPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func findRoute(routes []HTTPRoute, method, path string) (HTTPRoute, bool) {
	return indexRoutes(routes).find(method, path)
}

// skipAuthzPath is true for built-in Huma spec/docs/schema routes (always public).
func skipAuthzPath(path string) bool {
	p := normalizeRequestPath(path)
	switch {
	case strings.HasPrefix(p, "/huma/openapi"),
		strings.HasPrefix(p, "/openapi.json"),
		strings.HasPrefix(p, "/openapi.yaml"),
		strings.HasPrefix(p, "/openapi-3.0"),
		p == "/openapi":
		return true
	case p == "/docs", strings.HasPrefix(p, "/docs/"):
		return true
	case p == "/schemas", strings.HasPrefix(p, "/schemas/"):
		return true
	default:
		return false
	}
}

type routeIndex struct {
	byMethod map[string][]HTTPRoute
	any      []HTTPRoute
}

func indexRoutes(routes []HTTPRoute) routeIndex {
	idx := routeIndex{byMethod: map[string][]HTTPRoute{}}
	for _, r := range routes {
		m := strings.ToUpper(strings.TrimSpace(r.Method))
		if m == "" {
			idx.any = append(idx.any, r)
			continue
		}
		idx.byMethod[m] = append(idx.byMethod[m], r)
	}
	return idx
}

func (idx routeIndex) find(method, path string) (HTTPRoute, bool) {
	method = strings.ToUpper(strings.TrimSpace(method))
	var best HTTPRoute
	bestScore := -1
	consider := func(r HTTPRoute) {
		if !r.match(method, path) {
			return
		}
		score := routeSpecificity(r)
		if score > bestScore {
			best = r
			bestScore = score
		}
	}
	for _, r := range idx.byMethod[method] {
		consider(r)
	}
	for _, r := range idx.any {
		consider(r)
	}
	if bestScore < 0 {
		return HTTPRoute{}, false
	}
	return best, true
}

func routeSpecificity(r HTTPRoute) int {
	score := len(splitPath(r.Path)) * 10
	if r.Method != "" {
		score += 5
	}
	for _, seg := range splitPath(r.Path) {
		if !(strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")) {
			score++
		}
	}
	return score
}

// ProvideHTTPRoutes 将路由→permission 表发布到 "authz_http_routes" fx group。
// 构造函数须返回 []HTTPRoute。
//
//	authz.ProvideHTTPRoutes(func() []authz.HTTPRoute {
//	    return []authz.HTTPRoute{{Method: http.MethodGet, Path: "/users", Permission: "users:list"}}
//	})
func ProvideHTTPRoutes(fn any) fx.Option {
	return fx.Provide(fx.Annotate(fn, fx.ResultTags(`group:"authz_http_routes,flatten"`)))
}

// defaultHTTPRoutes 提供空默认；业务通过 [ProvideHTTPRoutes] 追加。
func defaultHTTPRoutes() []HTTPRoute {
	return nil
}

// Method helpers for route tables.
const (
	MethodGet    = http.MethodGet
	MethodPost   = http.MethodPost
	MethodPut    = http.MethodPut
	MethodPatch  = http.MethodPatch
	MethodDelete = http.MethodDelete
)
