package authz

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	fxhuma "github.com/fitan/fxkit/huma"
)

// Admin Huma operations — single source for OpenAPI + authz route catalog.
var (
	opListPermissions = huma.Operation{
		OperationID: "listAuthzPermissions", Method: http.MethodGet, Path: "/authz/permissions",
		Summary: "列出 API 权限目录", Description: "返回已登记的 path/method + Huma 元数据", Tags: []string{"Authz"},
	}
	opCreatePermission = huma.Operation{
		OperationID: "createAuthzPermission", Method: http.MethodPost, Path: "/authz/permissions",
		Summary: "创建权限目录项", Tags: []string{"Authz"},
	}
	opGetPermission = huma.Operation{
		OperationID: "getAuthzPermission", Method: http.MethodGet, Path: "/authz/permissions/{id}",
		Summary: "获取权限目录项", Tags: []string{"Authz"},
	}
	opUpdatePermission = huma.Operation{
		OperationID: "updateAuthzPermission", Method: http.MethodPut, Path: "/authz/permissions/{id}",
		Summary: "更新权限目录项", Tags: []string{"Authz"},
	}
	opDeletePermission = huma.Operation{
		OperationID: "deleteAuthzPermission", Method: http.MethodDelete, Path: "/authz/permissions/{id}",
		Summary: "删除权限目录项", Description: "仅删除目录元数据，不删除 Casbin 策略", Tags: []string{"Authz"},
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
	return HTTPRoutesFromOperations(
		opListPermissions, opCreatePermission, opGetPermission, opUpdatePermission, opDeletePermission,
		opListRoles, opCreateRole, opGetRole, opDeleteRole,
		opListRolePermissions, opSetRolePermissions, opAddRolePermission, opRemoveRolePermission,
		opListSubjectRoles, opSetSubjectRoles, opAddSubjectRole, opRemoveSubjectRole,
		opListBindings, opListPolicies,
	)
}

// Unique named types so Huma OpenAPI registry does not collide on Body schema names.
type permissionView struct {
	ID          uint     `json:"id"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Pattern     string   `json:"pattern"`
	OperationID string   `json:"operation_id"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
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

type permissionDetailOut struct {
	Body permissionView
}

type permissionBody struct {
	Method      string   `json:"method" minLength:"1" doc:"HTTP 方法，如 GET"`
	Path        string   `json:"path" minLength:"1" doc:"Huma 路径，如 /users/{id}"`
	OperationID string   `json:"operation_id,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}
type createPermissionIn struct {
	Body permissionBody
}
type updatePermissionIn struct {
	ID   uint `path:"id"`
	Body permissionBody
}
type permissionIDIn struct {
	ID uint `path:"id"`
}

func toPermissionView(r APIPermission) permissionView {
	return permissionView{
		ID: r.ID, Method: r.Method, Path: r.Path, Pattern: r.Pattern,
		OperationID: r.OperationID, Summary: r.Summary, Description: r.Description,
		Tags: splitTags(r.Tags),
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
	Items []RolePermission `json:"items"`
}
type setRolePermissionsIn struct {
	Role string `path:"role"`
	Body setRolePermissionsBody
}

type addRolePermissionBody struct {
	Path   string `json:"path" minLength:"1"`
	Method string `json:"method" minLength:"1"`
}
type addRolePermissionIn struct {
	Role string `path:"role"`
	Body addRolePermissionBody
}

type removeRolePermissionIn struct {
	Role   string `path:"role"`
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
	ID string `path:"id"`
}

type setSubjectRolesBody struct {
	Roles []string `json:"roles"`
}
type setSubjectRolesIn struct {
	ID   string `path:"id"`
	Body setSubjectRolesBody
}

type addSubjectRoleBody struct {
	Role string `json:"role" minLength:"1"`
}
type addSubjectRoleIn struct {
	ID   string `path:"id"`
	Body addSubjectRoleBody
}

type removeSubjectRoleIn struct {
	ID   string `path:"id"`
	Role string `path:"role"`
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

func provideAdminRegistrar(e *Enforcer) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		if e == nil || !e.Enabled() {
			return
		}
		registerPermissionCatalog(api, e)
		registerRoleAdmin(api, e)
		registerSubjectAdmin(api, e)
		registerOverview(api, e)
	})
}

func registerPermissionCatalog(api huma.API, e *Enforcer) {
	fxhuma.Register(api, opListPermissions, func(ctx context.Context, in *listPermissionsIn) (*listPermissionsOut, error) {
		page, err := e.ListRouteCatalogPage(ctx, ListRouteCatalogInput{
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
	fxhuma.Register(api, opCreatePermission, func(ctx context.Context, in *createPermissionIn) (*permissionDetailOut, error) {
		row, err := e.CreateAPIPermission(ctx, CreateAPIPermissionInput{
			Method: in.Body.Method, Path: in.Body.Path, OperationID: in.Body.OperationID,
			Summary: in.Body.Summary, Description: in.Body.Description, Tags: in.Body.Tags,
		})
		if err != nil {
			return nil, err
		}
		return &permissionDetailOut{Body: toPermissionView(*row)}, nil
	})
	fxhuma.Register(api, opGetPermission, func(ctx context.Context, in *permissionIDIn) (*permissionDetailOut, error) {
		row, err := e.GetAPIPermission(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return &permissionDetailOut{Body: toPermissionView(*row)}, nil
	})
	fxhuma.Register(api, opUpdatePermission, func(ctx context.Context, in *updatePermissionIn) (*permissionDetailOut, error) {
		row, err := e.UpdateAPIPermission(ctx, UpdateAPIPermissionInput{
			ID: in.ID, Method: in.Body.Method, Path: in.Body.Path, OperationID: in.Body.OperationID,
			Summary: in.Body.Summary, Description: in.Body.Description, Tags: in.Body.Tags,
		})
		if err != nil {
			return nil, err
		}
		return &permissionDetailOut{Body: toPermissionView(*row)}, nil
	})
	fxhuma.Register(api, opDeletePermission, func(ctx context.Context, in *permissionIDIn) (*struct{}, error) {
		if err := e.DeleteAPIPermission(ctx, DeleteAPIPermissionInput{ID: in.ID}); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
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
		if err := e.SetRolePermissions(ctx, SetRolePermissionsInput{Role: in.Role, Items: in.Body.Items}); err != nil {
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
			Role: in.Role, Path: in.Body.Path, Method: in.Body.Method,
		}); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
	})
	fxhuma.Register(api, opRemoveRolePermission, func(ctx context.Context, in *removeRolePermissionIn) (*struct{}, error) {
		if err := e.RemoveRolePermission(ctx, AddRolePermissionInput{
			Role: in.Role, Path: in.Path, Method: in.Method,
		}); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
	})
}

func registerSubjectAdmin(api huma.API, e *Enforcer) {
	fxhuma.Register(api, opListSubjectRoles, func(ctx context.Context, in *subjectPathIn) (*subjectRolesOut, error) {
		roles, err := e.ListSubjectRoles(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return &subjectRolesOut{Body: subjectRolesBody{Roles: roles}}, nil
	})
	fxhuma.Register(api, opSetSubjectRoles, func(ctx context.Context, in *setSubjectRolesIn) (*subjectRolesOut, error) {
		if err := e.SetSubjectRoles(ctx, SubjectRolesInput{Subject: in.ID, Roles: in.Body.Roles}); err != nil {
			return nil, err
		}
		roles, err := e.ListSubjectRoles(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return &subjectRolesOut{Body: subjectRolesBody{Roles: roles}}, nil
	})
	fxhuma.Register(api, opAddSubjectRole, func(ctx context.Context, in *addSubjectRoleIn) (*struct{}, error) {
		if err := e.AddSubjectRole(ctx, AddSubjectRoleInput{Subject: in.ID, Role: in.Body.Role}); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
	})
	fxhuma.Register(api, opRemoveSubjectRole, func(ctx context.Context, in *removeSubjectRoleIn) (*struct{}, error) {
		if err := e.RemoveSubjectRole(ctx, AddSubjectRoleInput{Subject: in.ID, Role: in.Role}); err != nil {
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

func splitTags(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
