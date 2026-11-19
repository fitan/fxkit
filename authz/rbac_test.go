package authz_test

import (
	"testing"

	"github.com/fitan/fxkit/authz"
)

func TestRBAC_RolePermissionsAndBindings(t *testing.T) {
	e, err := authz.NewMemoryEnforcer()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()

	if _, err := e.CreateRole(ctx, authz.CreateRoleInput{Name: "editor", DisplayName: "编辑"}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetRolePermissions(ctx, authz.SetRolePermissionsInput{
		Role: "editor",
		Items: []authz.RolePermission{
			{Path: "/users", Method: "GET"},
			{Path: "/users/{id}", Method: "GET"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	perms, err := e.ListRolePermissions(ctx, "editor")
	if err != nil || len(perms) != 2 {
		t.Fatalf("perms=%+v err=%v", perms, err)
	}
	if perms[0].Path != "/users" && perms[1].Path != "/users" {
		t.Fatalf("expected /users in %+v", perms)
	}
	// Huma 的 {id} 会存成 :id
	foundDetail := false
	for _, p := range perms {
		if p.Path == "/users/:id" && p.Method == "GET" {
			foundDetail = true
		}
	}
	if !foundDetail {
		t.Fatalf("expected /users/:id got %+v", perms)
	}

	if _, err := e.CreateRole(ctx, authz.CreateRoleInput{Name: "writer", DisplayName: "写"}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetRolePermissions(ctx, authz.SetRolePermissionsInput{
		Role:  "writer",
		Items: []authz.RolePermission{{Path: "/users", Method: "POST"}},
	}); err != nil {
		t.Fatal(err)
	}

	// 一个主体可以绑定多个角色；权限取并集。
	if err := e.SetSubjectRoles(ctx, authz.SubjectRolesInput{
		Subject: "bob",
		Roles:   []string{"editor", "writer", "editor"},
	}); err != nil {
		t.Fatal(err)
	}
	roles, err := e.ListSubjectRoles(ctx, "bob")
	if err != nil || len(roles) != 2 || roles[0] != "editor" || roles[1] != "writer" {
		t.Fatalf("roles=%v err=%v", roles, err)
	}
	ok, err := e.Enforce(ctx, authz.EnforceInput{Dom: authz.DefaultDomain, Sub: "bob", Obj: "/users", Act: "GET"})
	if err != nil || !ok {
		t.Fatalf("enforce list=%v err=%v", ok, err)
	}
	ok, err = e.Enforce(ctx, authz.EnforceInput{Dom: authz.DefaultDomain, Sub: "bob", Obj: "/users", Act: "POST"})
	if err != nil || !ok {
		t.Fatalf("bob with writer should POST: ok=%v err=%v", ok, err)
	}

	if err := e.SetSubjectRoles(ctx, authz.SubjectRolesInput{Subject: "bob", Roles: nil}); err != nil {
		t.Fatal(err)
	}
	ok, _ = e.Enforce(ctx, authz.EnforceInput{Dom: authz.DefaultDomain, Sub: "bob", Obj: "/users", Act: "GET"})
	if ok {
		t.Fatal("expected deny after clearing roles")
	}

	if err := e.DeleteRole(ctx, authz.DeleteRoleInput{Role: "editor"}); err != nil {
		t.Fatal(err)
	}
	perms, err = e.ListRolePermissions(ctx, "editor")
	if err != nil || len(perms) != 0 {
		t.Fatalf("after delete perms=%+v err=%v", perms, err)
	}
}

func TestEnforce_TrailingSlashNeedsNormalize(t *testing.T) {
	e, err := authz.NewMemoryEnforcer()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := e.AddPolicies([]authz.Policy{{Sub: "admin", Obj: "/users", Act: "GET"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.AddRoleBindings([]authz.RoleBinding{{User: "alice", Role: "admin"}}); err != nil {
		t.Fatal(err)
	}
	ok, err := e.Enforce(ctx, authz.EnforceInput{Dom: authz.DefaultDomain, Sub: "alice", Obj: "/users/", Act: "GET"})
	if err != nil || !ok {
		t.Fatalf("trailing slash should match /users: ok=%v err=%v", ok, err)
	}
}

// TestRBAC_GranularMutationsAndListing 覆盖单条 p/g 规则的新增、删除与列表。
func TestRBAC_GranularMutationsAndListing(t *testing.T) {
	e, err := authz.NewMemoryEnforcer()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()

	if err := e.AddRolePermission(ctx, authz.AddRolePermissionInput{
		Role: "editor", Path: "/articles/{id}", Method: "get",
	}); err != nil {
		t.Fatal(err)
	}
	policies, err := e.ListPolicies(ctx)
	if err != nil || len(policies) != 1 {
		t.Fatalf("policies=%+v err=%v", policies, err)
	}
	if policies[0].Sub != "editor" || policies[0].Obj != "/articles/:id" || policies[0].Act != "GET" {
		t.Fatalf("unexpected policy: %+v", policies[0])
	}

	// 参数校验：role/path/method 任一为空都拒绝。
	if err := e.AddRolePermission(ctx, authz.AddRolePermissionInput{Role: "", Path: "/x", Method: "GET"}); err == nil {
		t.Fatal("empty role must be rejected")
	}
	if err := e.RemoveRolePermission(ctx, authz.AddRolePermissionInput{Role: "", Path: "/x", Method: "GET"}); err == nil {
		t.Fatal("empty role must be rejected")
	}

	if err := e.RemoveRolePermission(ctx, authz.AddRolePermissionInput{
		Role: "editor", Path: "/articles/{id}", Method: "GET",
	}); err != nil {
		t.Fatal(err)
	}
	if policies, err = e.ListPolicies(ctx); err != nil || len(policies) != 0 {
		t.Fatalf("after remove policies=%+v err=%v", policies, err)
	}

	if err := e.AddSubjectRole(ctx, authz.AddSubjectRoleInput{Subject: "alice", Role: "editor"}); err != nil {
		t.Fatal(err)
	}
	bindings, err := e.ListBindings(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	if bindings[0].User != "alice" || bindings[0].Role != "editor" {
		t.Fatalf("unexpected binding: %+v", bindings[0])
	}
	if err := e.AddSubjectRole(ctx, authz.AddSubjectRoleInput{Subject: "alice", Role: ""}); err == nil {
		t.Fatal("empty role must be rejected")
	}

	if err := e.RemoveSubjectRole(ctx, authz.AddSubjectRoleInput{Subject: "alice", Role: "editor"}); err != nil {
		t.Fatal(err)
	}
	if bindings, err = e.ListBindings(ctx); err != nil || len(bindings) != 0 {
		t.Fatalf("after remove bindings=%+v err=%v", bindings, err)
	}
	if err := e.RemoveSubjectRole(ctx, authz.AddSubjectRoleInput{Subject: "", Role: "editor"}); err == nil {
		t.Fatal("empty subject must be rejected")
	}

	// 未启用的 enforcer 必须拒绝，而不是 panic。
	var disabled *authz.Enforcer
	if _, err := disabled.ListPolicies(ctx); err == nil {
		t.Fatal("disabled enforcer must return an error")
	}
	if _, err := disabled.ListBindings(ctx); err == nil {
		t.Fatal("disabled enforcer must return an error")
	}
}
