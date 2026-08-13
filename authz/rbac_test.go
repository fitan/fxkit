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
	// Huma {id} stored as :id
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

	// One subject may bind multiple roles; permissions are the union.
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
	ok, err := e.Enforce(ctx, authz.EnforceInput{Sub: "bob", Obj: "/users", Act: "GET"})
	if err != nil || !ok {
		t.Fatalf("enforce list=%v err=%v", ok, err)
	}
	ok, err = e.Enforce(ctx, authz.EnforceInput{Sub: "bob", Obj: "/users", Act: "POST"})
	if err != nil || !ok {
		t.Fatalf("bob with writer should POST: ok=%v err=%v", ok, err)
	}

	if err := e.SetSubjectRoles(ctx, authz.SubjectRolesInput{Subject: "bob", Roles: nil}); err != nil {
		t.Fatal(err)
	}
	ok, _ = e.Enforce(ctx, authz.EnforceInput{Sub: "bob", Obj: "/users", Act: "GET"})
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
