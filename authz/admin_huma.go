package authz

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	fxhuma "github.com/fitan/fxkit/huma"
)

// 管理端 Huma operation —— OpenAPI 与 authz 路由目录的唯一来源。
var (
	opListPermissions = huma.Operation{
		OperationID: "listAuthzPermissions", Method: http.MethodGet, Path: "/authz/permissions",
		Summary: "列出 API 权限目录", Description: "从路由表 + Casbin 策略派生（只读）", Tags: []string{"Authz"},
	}
	opListRoles = huma.Operation{
		OperationID: "listAuthzRoles", Method: http.MethodGet, Path: "/authz/roles",
		Summary: "列出角色", Tags: []string{"Authz"},
	}
	opCreateRole = huma.Operation{
		OperationID: "createAuthzRole", Method: http.MethodPost, Path: "/authz/roles",
		Summary: "创建/更新角色", Tags: []string{"Authz"},
	}
	opGetRole = huma.Operation{
		OperationID: "getAuthzRole", Method: http.MethodGet, Path: "/authz/roles/{role}",
		Summary: "获取角色", Tags: []string{"Authz"},
	}
	opDeleteRole = huma.Operation{
		OperationID: "deleteAuthzRole", Method: http.MethodDelete, Path: "/authz/roles/{role}",
		Summary: "删除角色", Description: "删除角色元数据及其全部权限与用户绑定", Tags: []string{"Authz"},
	}
	opListRolePermissions = huma.Operation{
		OperationID: "listAuthzRolePermissions", Method: http.MethodGet, Path: "/authz/roles/{role}/permissions",
		Summary: "列出角色权限", Tags: []string{"Authz"},
	}
	opSetRolePermissions = huma.Operation{
		OperationID: "setAuthzRolePermissions", Method: http.MethodPut, Path: "/authz/roles/{role}/permissions",
		Summary: "覆盖角色权限", Description: "全量替换该角色的 path/method 授权", Tags: []string{"Authz"},
	}
	opAddRolePermission = huma.Operation{
		OperationID: "addAuthzRolePermission", Method: http.MethodPost, Path: "/authz/roles/{role}/permissions",
		Summary: "为角色追加权限", Tags: []string{"Authz"},
	}
	opRemoveRolePermission = huma.Operation{
		OperationID: "removeAuthzRolePermission", Method: http.MethodDelete, Path: "/authz/roles/{role}/permissions",
		Summary: "移除角色权限", Tags: []string{"Authz"},
	}
	opListSubjectRoles = huma.Operation{
		OperationID: "listAuthzSubjectRoles", Method: http.MethodGet, Path: "/authz/subjects/{id}/roles",
		Summary: "列出用户角色", Tags: []string{"Authz"},
	}
	opSetSubjectRoles = huma.Operation{
		OperationID: "setAuthzSubjectRoles", Method: http.MethodPut, Path: "/authz/subjects/{id}/roles",
		Summary: "覆盖用户角色", Tags: []string{"Authz"},
	}
	opAddSubjectRole = huma.Operation{
		OperationID: "addAuthzSubjectRole", Method: http.MethodPost, Path: "/authz/subjects/{id}/roles",
		Summary: "为用户追加角色", Tags: []string{"Authz"},
	}
	opRemoveSubjectRole = huma.Operation{
		OperationID: "removeAuthzSubjectRole", Method: http.MethodDelete, Path: "/authz/subjects/{id}/roles/{role}",
		Summary: "移除用户角色", Tags: []string{"Authz"},
	}
	opListBindings = huma.Operation{
		OperationID: "listAuthzBindings", Method: http.MethodGet, Path: "/authz/bindings",
		Summary: "列出全部用户→角色绑定", Tags: []string{"Authz"},
	}
	opListPolicies = huma.Operation{
		OperationID: "listAuthzPolicies", Method: http.MethodGet, Path: "/authz/policies",
		Summary: "列出全部 Casbin 策略", Tags: []string{"Authz"},
	}
)

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
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Pattern     string   `json:"pattern"`
	OperationID string   `json:"operation_id"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Grants      []string `json:"grants"`
}

type listPermissionsBody struct {
	Items []permissionView `json:"items"`
	Total *int64           `json:"total,omitempty"`
	Start int              `json:"start"`
	Limit int              `json:"limit"`
}
type listPermissionsOut struct {
	Body listPermissionsBody
}

type listPermissionsIn struct {
	Q              string `query:"q" doc:"模糊搜索 method/path/summary 等"`
	Start          int    `query:"start" default:"0" minimum:"0"`
	Limit          int    `query:"limit" default:"20" minimum:"1" maximum:"1000"`
	ReplyWithCount bool   `query:"replyWithCount" default:"true"`
}

func toPermissionView(r APIPermission) permissionView {
	return permissionView{
		Method: r.Method, Path: r.Path, Pattern: r.Pattern,
		OperationID: r.OperationID, Summary: r.Summary, Description: r.Description,
		Tags: r.Tags, Grants: r.Grants,
	}
}

type listRolesBody struct {
	Items []RoleRecord `json:"items"`
}
type listRolesOut struct {
	Body listRolesBody
}

type roleDetailOut struct {
	Body RoleRecord
}

type createRoleBody struct {
	Name        string `json:"name" minLength:"1" doc:"角色名，如 admin"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}
type createRoleIn struct {
	Body createRoleBody
}

type rolePathIn struct {
	Role string `path:"role"`
}

type rolePermissionsBody struct {
	Items []RolePermission `json:"items"`
}
type rolePermissionsOut struct {
	Body rolePermissionsBody
}

type setRolePermissionsBody struct {
	Domain string           `json:"domain,omitempty" default:"*"`
	Items  []RolePermission `json:"items"`
}
type setRolePermissionsIn struct {
	Role string `path:"role"`
	Body setRolePermissionsBody
}

type addRolePermissionBody struct {
	Domain string `json:"domain,omitempty" default:"*"`
	Path   string `json:"path" minLength:"1"`
	Method string `json:"method" minLength:"1"`
}
type addRolePermissionIn struct {
	Role string `path:"role"`
	Body addRolePermissionBody
}

type removeRolePermissionIn struct {
	Role   string `path:"role"`
	Domain string `query:"domain" default:"*"`
	Path   string `query:"path" minLength:"1"`
	Method string `query:"method" minLength:"1"`
}

type subjectRolesBody struct {
	Roles []string `json:"roles"`
}
type subjectRolesOut struct {
	Body subjectRolesBody
}

type subjectPathIn struct {
	ID     string `path:"id"`
	Domain string `query:"domain" default:"*"`
}

type setSubjectRolesBody struct {
	Domain string   `json:"domain,omitempty" default:"*"`
	Roles  []string `json:"roles"`
}
type setSubjectRolesIn struct {
	ID   string `path:"id"`
	Body setSubjectRolesBody
}

type addSubjectRoleBody struct {
	Domain string `json:"domain,omitempty" default:"*"`
	Role   string `json:"role" minLength:"1"`
}
type addSubjectRoleIn struct {
	ID   string `path:"id"`
	Body addSubjectRoleBody
}

type removeSubjectRoleIn struct {
	ID     string `path:"id"`
	Role   string `path:"role"`
	Domain string `query:"domain" default:"*"`
}

type listBindingsBody struct {
	Items []RoleBinding `json:"items"`
}
type listBindingsOut struct {
	Body listBindingsBody
}

type listPoliciesBody struct {
	Items []Policy `json:"items"`
}
type listPoliciesOut struct {
	Body listPoliciesBody
}

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
