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
// 鉴权只在 chi 中间件里做：Casbin 启用时按请求 path + METHOD Enforce，
// 关闭时登录即可。匿名路径写在 auth.public_paths。
//
// 推荐用 [HTTPRouteFromOperation] / [HTTPRoutesFromOperations] 从 huma.Operation 生成，
// 这样 OperationID / Summary / Tags 会写入 authz_api_permission 目录表。
//
//	authz.HTTPRoutesFromOperations(huma.Operation{
//	    OperationID: "listUsers", Method: "GET", Path: "/users", Summary: "列出用户",
//	})
type HTTPRoute struct {
	// Method HTTP 方法（如 GET）；空表示任意方法。
	Method string
	// Path 路由路径模板，与 Huma operation 一致，支持 `{id}` 占位。
	Path string
	// OperationID 是 Huma operation 的 ID，会写入权限目录。
	OperationID string // Huma OperationID
	// Summary 是 Huma summary，写入权限目录供展示。
	Summary string // Huma Summary（权限中文/说明）
	// Description 是 Huma description，写入权限目录。
	Description string // Huma Description
	// Tags 是 Huma tags，写入权限目录。
	Tags []string // Huma Tags
}

// normalizeRequestPath 去掉首尾空白与末尾斜杠，使鉴权和 Casbin
// Enforce 看到同一条路径（GET /users/ ≡ /users）。
func normalizeRequestPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

// skipAuthzPath 对探针、文档和 OpenAPI spec 返回 true（始终公开）。
//
// `.` 和 `-` 只属于 spec 文件名（/openapi.json、/openapi-3.0.json）。
// 用在 /docs、/version、/healthz 上会把 /docs-admin、/version-history 也放行。
func skipAuthzPath(path string) bool {
	p := normalizeRequestPath(path)
	switch p {
	case "/healthz", "/readyz", "/version":
		return true
	}
	if p == "/docs" || strings.HasPrefix(p, "/docs/") {
		return true
	}
	if p == "/schemas" || strings.HasPrefix(p, "/schemas/") {
		return true
	}
	return openAPISpecPath(p, "/openapi") || openAPISpecPath(p, "/huma/openapi")
}

// openAPISpecPath 只放行 Huma / docs 实际提供的 spec 文件。
// /openapi/ 下没有框架路由，/openapi/v1 这类业务路径必须继续鉴权。
func openAPISpecPath(p, prefix string) bool {
	switch p {
	case prefix + ".json", prefix + ".yaml",
		prefix + "-3.0.json", prefix + "-3.0.yaml":
		return true
	default:
		return false
	}
}

// ProvideHTTPRoutes 把路由登记进容器，供 Casbin 自动注册和权限目录使用。
//
//	authz.ProvideHTTPRoutes(func() []authz.HTTPRoute {
//	    return []authz.HTTPRoute{{Method: http.MethodGet, Path: "/users"}}
//	})
//
// 需要依赖时用 [ProvideHTTPRoutesLazy]。
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
	// MethodGet 是 GET 方法常量。
	MethodGet = http.MethodGet
	// MethodPost 是 POST 方法常量。
	MethodPost = http.MethodPost
	// MethodPut 是 PUT 方法常量。
	MethodPut = http.MethodPut
	// MethodPatch 是 PATCH 方法常量。
	MethodPatch = http.MethodPatch
	// MethodDelete 是 DELETE 方法常量。
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
			if skipAuthzPath(cp.Path) {
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

// mergeHTTPRoutes 按 method+path 去重合并两组路由，base 中的项优先。
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
