package authz

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	fxhuma "github.com/fitan/fxkit/huma"
)

// 管理端 Huma operation —— OpenAPI 与 authz 路由目录的唯一来源。
var (
	// opListPermissions 是 GET /authz/permissions（listAuthzPermissions）的 operation。
	opListPermissions = huma.Operation{
		OperationID: "listAuthzPermissions", Method: http.MethodGet, Path: "/authz/permissions",
		Summary: "列出 API 权限目录", Description: "从路由表 + Casbin 策略派生（只读）", Tags: []string{"Authz"},
	}
	// opListRoles 是 GET /authz/roles（listAuthzRoles）的 operation。
	opListRoles = huma.Operation{
		OperationID: "listAuthzRoles", Method: http.MethodGet, Path: "/authz/roles",
		Summary: "列出角色", Tags: []string{"Authz"},
	}
	// opCreateRole 是 POST /authz/roles（createAuthzRole）的 operation。
	opCreateRole = huma.Operation{
		OperationID: "createAuthzRole", Method: http.MethodPost, Path: "/authz/roles",
		Summary: "创建/更新角色", Tags: []string{"Authz"},
	}
	// opGetRole 是 GET /authz/roles/{role}（getAuthzRole）的 operation。
	opGetRole = huma.Operation{
		OperationID: "getAuthzRole", Method: http.MethodGet, Path: "/authz/roles/{role}",
		Summary: "获取角色", Tags: []string{"Authz"},
	}
	// opDeleteRole 是 DELETE /authz/roles/{role}（deleteAuthzRole）的 operation。
	opDeleteRole = huma.Operation{
		OperationID: "deleteAuthzRole", Method: http.MethodDelete, Path: "/authz/roles/{role}",
		Summary: "删除角色", Description: "删除角色元数据及其全部权限与用户绑定", Tags: []string{"Authz"},
	}
	// opListRolePermissions 是 GET /authz/roles/{role}/permissions（listAuthzRolePermissions）的 operation。
	opListRolePermissions = huma.Operation{
		OperationID: "listAuthzRolePermissions", Method: http.MethodGet, Path: "/authz/roles/{role}/permissions",
		Summary: "列出角色权限", Tags: []string{"Authz"},
	}
	// opSetRolePermissions 是 PUT /authz/roles/{role}/permissions（setAuthzRolePermissions）的 operation。
	opSetRolePermissions = huma.Operation{
		OperationID: "setAuthzRolePermissions", Method: http.MethodPut, Path: "/authz/roles/{role}/permissions",
		Summary: "覆盖角色权限", Description: "全量替换该角色的 path/method 授权", Tags: []string{"Authz"},
	}
	// opAddRolePermission 是 POST /authz/roles/{role}/permissions（addAuthzRolePermission）的 operation。
	opAddRolePermission = huma.Operation{
		OperationID: "addAuthzRolePermission", Method: http.MethodPost, Path: "/authz/roles/{role}/permissions",
		Summary: "为角色追加权限", Tags: []string{"Authz"},
	}
	// opRemoveRolePermission 是 DELETE /authz/roles/{role}/permissions（removeAuthzRolePermission）的 operation。
	opRemoveRolePermission = huma.Operation{
		OperationID: "removeAuthzRolePermission", Method: http.MethodDelete, Path: "/authz/roles/{role}/permissions",
		Summary: "移除角色权限", Tags: []string{"Authz"},
	}
	// opListSubjectRoles 是 GET /authz/subjects/{id}/roles（listAuthzSubjectRoles）的 operation。
	opListSubjectRoles = huma.Operation{
		OperationID: "listAuthzSubjectRoles", Method: http.MethodGet, Path: "/authz/subjects/{id}/roles",
		Summary: "列出用户角色", Tags: []string{"Authz"},
	}
	// opSetSubjectRoles 是 PUT /authz/subjects/{id}/roles（setAuthzSubjectRoles）的 operation。
	opSetSubjectRoles = huma.Operation{
		OperationID: "setAuthzSubjectRoles", Method: http.MethodPut, Path: "/authz/subjects/{id}/roles",
		Summary: "覆盖用户角色", Tags: []string{"Authz"},
	}
	// opAddSubjectRole 是 POST /authz/subjects/{id}/roles（addAuthzSubjectRole）的 operation。
	opAddSubjectRole = huma.Operation{
		OperationID: "addAuthzSubjectRole", Method: http.MethodPost, Path: "/authz/subjects/{id}/roles",
		Summary: "为用户追加角色", Tags: []string{"Authz"},
	}
	// opRemoveSubjectRole 是 DELETE /authz/subjects/{id}/roles/{role}（removeAuthzSubjectRole）的 operation。
	opRemoveSubjectRole = huma.Operation{
		OperationID: "removeAuthzSubjectRole", Method: http.MethodDelete, Path: "/authz/subjects/{id}/roles/{role}",
		Summary: "移除用户角色", Tags: []string{"Authz"},
	}
	// opListBindings 是 GET /authz/bindings（listAuthzBindings）的 operation。
	opListBindings = huma.Operation{
		OperationID: "listAuthzBindings", Method: http.MethodGet, Path: "/authz/bindings",
		Summary: "列出全部用户→角色绑定", Tags: []string{"Authz"},
	}
	// opListPolicies 是 GET /authz/policies（listAuthzPolicies）的 operation。
	opListPolicies = huma.Operation{
		OperationID: "listAuthzPolicies", Method: http.MethodGet, Path: "/authz/policies",
		Summary: "列出全部 Casbin 策略", Tags: []string{"Authz"},
	}
)

// adminHTTPRoutes 把管理端 Huma operation 转换为 authz 路由目录项。
func adminHTTPRoutes() []HTTPRoute {
	routes := HTTPRoutesFromOperations(
		opListPermissions,
		opListRoles, opCreateRole, opGetRole, opDeleteRole,
		opListRolePermissions, opSetRolePermissions, opAddRolePermission, opRemoveRolePermission,
		opListSubjectRoles, opSetSubjectRoles, opAddSubjectRole, opRemoveSubjectRole,
		opListBindings, opListPolicies,
	)
	return routes
}

// 使用独立的具名类型，避免 Huma OpenAPI registry 在 Body schema 名上冲突。
// permissionView 是目录项的 API 表示（派生视图，无数据库 id）。
type permissionView struct {
	// Method 路由的 HTTP 方法（json: method，已转大写）。
	Method string `json:"method"`
	// Path 路由路径模板（json: path），可能含 Huma 的 `{id}` 占位符。
	Path string `json:"path"`
	// Pattern 转成 Casbin keyMatch2 后的路径（json: pattern，`{id}` → `:id`）。
	Pattern string `json:"pattern"`
	// OperationID Huma operation 的 ID（json: operation_id）。
	OperationID string `json:"operation_id"`
	// Summary Huma summary（json: summary），供目录展示。
	Summary string `json:"summary"`
	// Description Huma description（json: description），供目录展示。
	Description string `json:"description"`
	// Tags 路由所属的 Huma tags（json: tags）。
	Tags []string `json:"tags"`
	// Grants 被授予该 (method, pattern) 的角色/主体（json: grants），来自 Casbin p 规则；Casbin 关闭时为空。
	Grants []string `json:"grants"`
}

// listPermissionsBody 是权限目录分页响应体（json: items/total/start/limit）。
type listPermissionsBody struct {
	// Items 当前页的目录项（json: items）。
	Items []permissionView `json:"items"`
	// Total 过滤后的总条数（json: total）；仅 replyWithCount=true 时返回，否则省略。
	Total *int64 `json:"total,omitempty"`
	// Start 实际生效的起始偏移（json: start）。
	Start int `json:"start"`
	// Limit 实际生效的每页条数（json: limit），默认 20、上限 1000。
	Limit int `json:"limit"`
}

// listPermissionsOut 是列出权限目录的响应外壳。
type listPermissionsOut struct {
	// Body 响应体，序列化后字段平铺在 JSON 顶层。
	Body listPermissionsBody
}

// listPermissionsIn 是列出权限目录的查询参数。
type listPermissionsIn struct {
	// Q 模糊搜索关键字（query: q），匹配 method/path/summary 等字段；空表示不过滤。
	Q string `query:"q" doc:"模糊搜索 method/path/summary 等"`
	// Start 分页起始偏移（query: start），默认 0，不能为负。
	Start int `query:"start" default:"0" minimum:"0"`
	// Limit 每页条数（query: limit），默认 20，范围 1–1000。
	Limit int `query:"limit" default:"20" minimum:"1" maximum:"1000"`
	// ReplyWithCount 是否返回过滤后的总数（query: replyWithCount），默认 true。
	ReplyWithCount bool `query:"replyWithCount" default:"true"`
}

// toPermissionView 把目录项转换为管理端 API 视图。
func toPermissionView(r APIPermission) permissionView {
	return permissionView{
		Method: r.Method, Path: r.Path, Pattern: r.Pattern,
		OperationID: r.OperationID, Summary: r.Summary, Description: r.Description,
		Tags: r.Tags, Grants: r.Grants,
	}
}

// listRolesBody 是角色列表响应体（json: items）。
type listRolesBody struct {
	// Items 全部角色元数据（json: items），按角色名升序。
	Items []RoleRecord `json:"items"`
}

// listRolesOut 是列出角色的响应外壳。
type listRolesOut struct {
	// Body 响应体，序列化后字段平铺在 JSON 顶层。
	Body listRolesBody
}

// roleDetailOut 是单个角色的响应外壳（创建与查询共用）。
type roleDetailOut struct {
	// Body 角色元数据，序列化后字段平铺在 JSON 顶层。
	Body RoleRecord
}

// createRoleBody 是创建/更新角色的请求体（json: name/display_name/description）。
type createRoleBody struct {
	// Name 角色名（json: name），必填，至少 1 个字符。
	Name string `json:"name" minLength:"1" doc:"角色名，如 admin"`
	// DisplayName 展示名（json: display_name），可省略，为空时回退为 Name。
	DisplayName string `json:"display_name,omitempty"`
	// Description 角色描述（json: description），可省略。
	Description string `json:"description,omitempty"`
}

// createRoleIn 是创建角色的请求。
type createRoleIn struct {
	// Body 请求体（json: name/display_name/description）。
	Body createRoleBody
}

// rolePathIn 是带角色名的路径参数。
type rolePathIn struct {
	// Role 角色名（path: role）。
	Role string `path:"role"`
}

// rolePermissionsBody 是角色权限列表响应体（json: items）。
type rolePermissionsBody struct {
	// Items 角色的 path+method 权限（json: items），按 domain/path/method 排序。
	Items []RolePermission `json:"items"`
}

// rolePermissionsOut 是角色权限的响应外壳。
type rolePermissionsOut struct {
	// Body 响应体，序列化后字段平铺在 JSON 顶层。
	Body rolePermissionsBody
}

// setRolePermissionsBody 是全量覆盖角色权限的请求体。
type setRolePermissionsBody struct {
	// Domain 租户 domain（json: domain），默认 "*"（全局）。
	Domain string `json:"domain,omitempty" default:"*"`
	// Items 该角色在该 domain 下的完整权限列表（json: items），会替换旧规则；空表示清空。
	Items []RolePermission `json:"items"`
}

// setRolePermissionsIn 是覆盖角色权限的请求。
type setRolePermissionsIn struct {
	// Role 角色名（path: role）。
	Role string `path:"role"`
	// Body 请求体，包含 domain 与权限列表。
	Body setRolePermissionsBody
}

// addRolePermissionBody 是为角色追加单条权限的请求体。
type addRolePermissionBody struct {
	// Domain 租户 domain（json: domain），默认 "*"（全局）。
	Domain string `json:"domain,omitempty" default:"*"`
	// Path 权限路径（json: path），必填，支持 Huma 的 `{id}` 或 Casbin 的 `:id`。
	Path string `json:"path" minLength:"1"`
	// Method HTTP 方法（json: method），必填，写入前转大写。
	Method string `json:"method" minLength:"1"`
}

// addRolePermissionIn 是追加角色权限的请求。
type addRolePermissionIn struct {
	// Role 角色名（path: role）。
	Role string `path:"role"`
	// Body 请求体，包含 domain/path/method。
	Body addRolePermissionBody
}

// removeRolePermissionIn 是移除角色权限的请求，参数走路径与查询串。
type removeRolePermissionIn struct {
	// Role 角色名（path: role）。
	Role string `path:"role"`
	// Domain 租户 domain（query: domain），默认 "*"（全局）。
	Domain string `query:"domain" default:"*"`
	// Path 权限路径（query: path），必填。
	Path string `query:"path" minLength:"1"`
	// Method HTTP 方法（query: method），必填，比较前转大写。
	Method string `query:"method" minLength:"1"`
}

// subjectRolesBody 是主体角色列表响应体（json: roles）。
type subjectRolesBody struct {
	// Roles 主体在指定 domain 的角色名（json: roles），按字典序排序。
	Roles []string `json:"roles"`
}

// subjectRolesOut 是主体角色的响应外壳。
type subjectRolesOut struct {
	// Body 响应体，序列化后字段平铺在 JSON 顶层。
	Body subjectRolesBody
}

// subjectPathIn 是带主体 ID 的路径参数与可选 domain 查询参数。
type subjectPathIn struct {
	// ID 主体标识（path: id）。
	ID string `path:"id"`
	// Domain 租户 domain（query: domain），默认 "*"（全局）。
	Domain string `query:"domain" default:"*"`
}

// setSubjectRolesBody 是全量覆盖主体角色的请求体。
type setSubjectRolesBody struct {
	// Domain 租户 domain（json: domain），默认 "*"（全局）。
	Domain string `json:"domain,omitempty" default:"*"`
	// Roles 主体在该 domain 的完整角色列表（json: roles），会替换旧绑定；空表示全部解绑。
	Roles []string `json:"roles"`
}

// setSubjectRolesIn 是覆盖主体角色的请求。
type setSubjectRolesIn struct {
	// ID 主体标识（path: id）。
	ID string `path:"id"`
	// Body 请求体，包含 domain 与角色列表。
	Body setSubjectRolesBody
}

// addSubjectRoleBody 是为主体追加单个角色的请求体。
type addSubjectRoleBody struct {
	// Domain 租户 domain（json: domain），默认 "*"（全局）。
	Domain string `json:"domain,omitempty" default:"*"`
	// Role 要追加的角色名（json: role），必填。
	Role string `json:"role" minLength:"1"`
}

// addSubjectRoleIn 是追加主体角色的请求。
type addSubjectRoleIn struct {
	// ID 主体标识（path: id）。
	ID string `path:"id"`
	// Body 请求体，包含 domain 与角色名。
	Body addSubjectRoleBody
}

// removeSubjectRoleIn 是移除主体角色的请求，角色走路径、domain 走查询串。
type removeSubjectRoleIn struct {
	// ID 主体标识（path: id）。
	ID string `path:"id"`
	// Role 要移除的角色名（path: role）。
	Role string `path:"role"`
	// Domain 租户 domain（query: domain），默认 "*"（全局）。
	Domain string `query:"domain" default:"*"`
}

// listBindingsBody 是用户→角色绑定列表响应体（json: items）。
type listBindingsBody struct {
	// Items 全部 g 规则（json: items），按 user/domain/role 排序。
	Items []RoleBinding `json:"items"`
}

// listBindingsOut 是绑定列表的响应外壳。
type listBindingsOut struct {
	// Body 响应体，序列化后字段平铺在 JSON 顶层。
	Body listBindingsBody
}

// listPoliciesBody 是 Casbin 策略列表响应体（json: items）。
type listPoliciesBody struct {
	// Items 全部 p 规则（json: items）。
	Items []Policy `json:"items"`
}

// listPoliciesOut 是策略列表的响应外壳。
type listPoliciesOut struct {
	// Body 响应体，序列化后字段平铺在 JSON 顶层。
	Body listPoliciesBody
}

// provideAdminRegistrar 构造管理端 Registrar；执行器未启用时不注册任何 operation。
func provideAdminRegistrar(e *Enforcer, routes func() []HTTPRoute) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		if e == nil || !e.Enabled() {
			return
		}
		registerPermissionCatalog(api, e, routes)
		registerRoleAdmin(api, e)
		registerSubjectAdmin(api, e)
		registerOverview(api, e)
	})
}

// registerPermissionCatalog 只提供只读列表：目录是从路由表 + Casbin 策略派生的，
// 没有可写的独立实体，因此没有 create/update/delete。
func registerPermissionCatalog(api huma.API, e *Enforcer, routes func() []HTTPRoute) {
	fxhuma.Register(api, opListPermissions, func(ctx context.Context, in *listPermissionsIn) (*listPermissionsOut, error) {
		var rs []HTTPRoute
		if routes != nil {
			rs = routes()
		}
		page, err := e.ListRouteCatalogPage(ctx, rs, ListRouteCatalogInput{
			Start: in.Start, Limit: in.Limit, Q: in.Q, ReplyWithCount: in.ReplyWithCount,
		})
		if err != nil {
			return nil, err
		}
		resp := &listPermissionsOut{}
		resp.Body.Start = page.Start
		resp.Body.Limit = page.Limit
		resp.Body.Total = page.Total
		resp.Body.Items = make([]permissionView, 0, len(page.Items))
		for _, r := range page.Items {
			resp.Body.Items = append(resp.Body.Items, toPermissionView(r))
		}
		return resp, nil
	})
}

// registerRoleAdmin 注册角色及其权限的列表、创建、查询、删除与增删改 operation。
func registerRoleAdmin(api huma.API, e *Enforcer) {
	fxhuma.Register(api, opListRoles, func(ctx context.Context, _ *struct{}) (*listRolesOut, error) {
		items, err := e.ListRoles(ctx)
		if err != nil {
			return nil, err
		}
		return &listRolesOut{Body: listRolesBody{Items: items}}, nil
	})
	fxhuma.Register(api, opCreateRole, func(ctx context.Context, in *createRoleIn) (*roleDetailOut, error) {
		row, err := e.CreateRole(ctx, CreateRoleInput{
			Name: in.Body.Name, DisplayName: in.Body.DisplayName, Description: in.Body.Description,
		})
		if err != nil {
			return nil, err
		}
		return &roleDetailOut{Body: *row}, nil
	})
	fxhuma.Register(api, opGetRole, func(ctx context.Context, in *rolePathIn) (*roleDetailOut, error) {
		row, err := e.GetRole(ctx, in.Role)
		if err != nil {
			return nil, err
		}
		return &roleDetailOut{Body: *row}, nil
	})
	fxhuma.Register(api, opDeleteRole, func(ctx context.Context, in *rolePathIn) (*struct{}, error) {
		if err := e.DeleteRole(ctx, DeleteRoleInput{Role: in.Role}); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
	})
	fxhuma.Register(api, opListRolePermissions, func(ctx context.Context, in *rolePathIn) (*rolePermissionsOut, error) {
		items, err := e.ListRolePermissions(ctx, in.Role)
		if err != nil {
			return nil, err
		}
		return &rolePermissionsOut{Body: rolePermissionsBody{Items: items}}, nil
	})
	fxhuma.Register(api, opSetRolePermissions, func(ctx context.Context, in *setRolePermissionsIn) (*rolePermissionsOut, error) {
		if err := e.SetRolePermissions(ctx, SetRolePermissionsInput{Role: in.Role, Domain: in.Body.Domain, Items: in.Body.Items}); err != nil {
			return nil, err
		}
		items, err := e.ListRolePermissions(ctx, in.Role)
		if err != nil {
			return nil, err
		}
		return &rolePermissionsOut{Body: rolePermissionsBody{Items: items}}, nil
	})
	fxhuma.Register(api, opAddRolePermission, func(ctx context.Context, in *addRolePermissionIn) (*struct{}, error) {
		if err := e.AddRolePermission(ctx, AddRolePermissionInput{
			Role: in.Role, Domain: in.Body.Domain, Path: in.Body.Path, Method: in.Body.Method,
		}); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
	})
	fxhuma.Register(api, opRemoveRolePermission, func(ctx context.Context, in *removeRolePermissionIn) (*struct{}, error) {
		if err := e.RemoveRolePermission(ctx, AddRolePermissionInput{
			Role: in.Role, Domain: in.Domain, Path: in.Path, Method: in.Method,
		}); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
	})
}

// registerSubjectAdmin 注册主体→角色绑定的列表、覆盖、追加与移除 operation。
func registerSubjectAdmin(api huma.API, e *Enforcer) {
	fxhuma.Register(api, opListSubjectRoles, func(ctx context.Context, in *subjectPathIn) (*subjectRolesOut, error) {
		roles, err := e.ListSubjectRolesInDomain(ctx, in.ID, in.Domain)
		if err != nil {
			return nil, err
		}
		return &subjectRolesOut{Body: subjectRolesBody{Roles: roles}}, nil
	})
	fxhuma.Register(api, opSetSubjectRoles, func(ctx context.Context, in *setSubjectRolesIn) (*subjectRolesOut, error) {
		if err := e.SetSubjectRoles(ctx, SubjectRolesInput{Subject: in.ID, Domain: in.Body.Domain, Roles: in.Body.Roles}); err != nil {
			return nil, err
		}
		roles, err := e.ListSubjectRolesInDomain(ctx, in.ID, in.Body.Domain)
		if err != nil {
			return nil, err
		}
		return &subjectRolesOut{Body: subjectRolesBody{Roles: roles}}, nil
	})
	fxhuma.Register(api, opAddSubjectRole, func(ctx context.Context, in *addSubjectRoleIn) (*struct{}, error) {
		if err := e.AddSubjectRole(ctx, AddSubjectRoleInput{Subject: in.ID, Domain: in.Body.Domain, Role: in.Body.Role}); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
	})
	fxhuma.Register(api, opRemoveSubjectRole, func(ctx context.Context, in *removeSubjectRoleIn) (*struct{}, error) {
		if err := e.RemoveSubjectRole(ctx, AddSubjectRoleInput{Subject: in.ID, Domain: in.Domain, Role: in.Role}); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
	})
}

// registerOverview 注册只读的绑定列表与策略列表 operation。
func registerOverview(api huma.API, e *Enforcer) {
	fxhuma.Register(api, opListBindings, func(ctx context.Context, _ *struct{}) (*listBindingsOut, error) {
		items, err := e.ListBindings(ctx)
		if err != nil {
			return nil, err
		}
		return &listBindingsOut{Body: listBindingsBody{Items: items}}, nil
	})
	fxhuma.Register(api, opListPolicies, func(ctx context.Context, _ *struct{}) (*listPoliciesOut, error) {
		items, err := e.ListPolicies(ctx)
		if err != nil {
			return nil, err
		}
		return &listPoliciesOut{Body: listPoliciesBody{Items: items}}, nil
	})
}
