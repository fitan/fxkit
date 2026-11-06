package authz

import (
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/di"
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
	Permission  string   // 遗留字段：仅在 Casbin 关闭时使用
	OperationID string   // Huma OperationID
	Summary     string   // Huma Summary（权限中文/说明）
	Description string   // Huma Description
	Tags        []string // Huma Tags
}

// normalizeRequestPath 去掉首尾空白与末尾斜杠，使 findRoute 与 Casbin
// Enforce 看到同一条路径（GET /users/ ≡ /users）。
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

// skipAuthzPath 对 Huma 内置的 spec/docs/schema 路由返回 true（始终公开）。
//
// 必须按「路径段」匹配：早期的 strings.HasPrefix(p, "/docs") 会把 /docs-evil、
// /openapix 这类完全无关的路径也当成公开路径放行。
func skipAuthzPath(path string) bool {
	p := normalizeRequestPath(path)
	for _, prefix := range []string{
		"/openapi",      // /openapi, /openapi.json, /openapi.yaml, /openapi-3.0.json
		"/huma/openapi", // /huma/openapi.json / .yaml / -3.0.json
		"/docs",         // Scalar UI
		"/schemas",      // Huma JSON-schema
	} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") || strings.HasPrefix(p, prefix+".") || strings.HasPrefix(p, prefix+"-") {
			return true
		}
	}
	return false
}

type routeIndex struct {
	byMethod map[string]*methodRoutes
	any      *methodRoutes
}

// methodRoutes 按「首段」索引，避免每条候选路由都从头扫路径。
type methodRoutes struct {
	byFirst    map[string][]compiledRoute
	paramFirst []compiledRoute
}

func newMethodRoutes() *methodRoutes {
	return &methodRoutes{byFirst: map[string][]compiledRoute{}}
}

func (m *methodRoutes) add(c compiledRoute) {
	if len(c.segs) > 0 && !c.param[0] {
		m.byFirst[c.segs[0]] = append(m.byFirst[c.segs[0]], c)
		return
	}
	m.paramFirst = append(m.paramFirst, c)
}

func (m *methodRoutes) consider(path string, reqSegs int, fn func(compiledRoute)) {
	first := path
	if j := strings.IndexByte(path, '/'); j >= 0 {
		first = path[:j]
	}
	for _, c := range m.byFirst[first] {
		if len(c.segs) == reqSegs {
			fn(c)
		}
	}
	for _, c := range m.paramFirst {
		if len(c.segs) == reqSegs {
			fn(c)
		}
	}
}

// compiledRoute 预计算路径分段与匹配权重，避免每个请求对每条候选路由重复 split。
type compiledRoute struct {
	route       HTTPRoute
	method      string
	segs        []string
	param       []bool
	specificity int
}

func compileRoute(r HTTPRoute) compiledRoute {
	m := strings.ToUpper(strings.TrimSpace(r.Method))
	segs := splitPath(normalizeRequestPath(r.Path))
	param := make([]bool, len(segs))
	weight := len(segs) * 10
	if m != "" {
		weight += 5
	}
	for i, s := range segs {
		param[i] = strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")
		if !param[i] {
			weight++
		}
	}
	return compiledRoute{route: r, method: m, segs: segs, param: param, specificity: weight}
}

// matchPath 逐段匹配已规范化的路径（已 Trim 首尾 "/"），不分配。
func (c compiledRoute) matchPath(p string) bool {
	idx := 0
	for {
		if idx >= len(c.segs) {
			return false
		}
		seg := p
		if j := strings.IndexByte(p, '/'); j >= 0 {
			seg = p[:j]
			p = p[j+1:]
		} else {
			p = ""
		}
		if !c.param[idx] && c.segs[idx] != seg {
			return false
		}
		idx++
		if p == "" {
			return idx == len(c.segs)
		}
	}
}

func indexRoutes(routes []HTTPRoute) routeIndex {
	idx := routeIndex{byMethod: map[string]*methodRoutes{}}
	for _, r := range routes {
		c := compileRoute(r)
		if c.method == "" {
			if idx.any == nil {
				idx.any = newMethodRoutes()
			}
			idx.any.add(c)
			continue
		}
		mr := idx.byMethod[c.method]
		if mr == nil {
			mr = newMethodRoutes()
			idx.byMethod[c.method] = mr
		}
		mr.add(c)
	}
	return idx
}

func (idx routeIndex) find(method, path string) (HTTPRoute, bool) {
	method = strings.ToUpper(strings.TrimSpace(method))
	// 规范化一次，后续所有候选路由共用；先按段数/首段过滤，再做逐段比较。
	path = strings.Trim(normalizeRequestPath(path), "/")
	reqSegs := 0
	if path != "" {
		reqSegs = strings.Count(path, "/") + 1
	}
	var best HTTPRoute
	bestScore := -1
	consider := func(c compiledRoute) {
		if !c.matchPath(path) {
			return
		}
		if c.specificity > bestScore {
			best = c.route
			bestScore = c.specificity
		}
	}
	if mr := idx.byMethod[method]; mr != nil {
		mr.consider(path, reqSegs, consider)
	}
	if idx.any != nil {
		idx.any.consider(path, reqSegs, consider)
	}
	if bestScore < 0 {
		return HTTPRoute{}, false
	}
	return best, true
}

// ProvideHTTPRoutes 将路由→permission 表发布到 "authz_http_routes" fx group。
// 构造函数须返回 []HTTPRoute。
//
//	authz.ProvideHTTPRoutes(func() []authz.HTTPRoute {
//	    return []authz.HTTPRoute{{Method: http.MethodGet, Path: "/users", Permission: "users:list"}}
//	})
//
// 也支持 func(*Service) []HTTPRoute（延迟到 PreStart 装配）与 []HTTPRoute 直接发布。
// ProvideHTTPRoutes 发布若干 [HTTPRoute]。需要依赖时用 [ProvideHTTPRoutesLazy]。
func ProvideHTTPRoutes(routes ...HTTPRoute) di.Option {
	return func(i di.Injector) {
		if len(routes) == 0 {
			return
		}
		di.ProvideGroupItems(i, routes...)
	}
}

// ProvideHTTPRoutesLazy 在 PreStart 求值（可访问完整依赖图）。
func ProvideHTTPRoutesLazy(fn func(di.Injector) ([]HTTPRoute, error)) di.Option {
	return func(i di.Injector) { di.ProvideLazyE[[]HTTPRoute](i, fn) }
}

// 路由表使用的 Method 常量。
const (
	MethodGet    = http.MethodGet
	MethodPost   = http.MethodPost
	MethodPut    = http.MethodPut
	MethodPatch  = http.MethodPatch
	MethodDelete = http.MethodDelete
)

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
