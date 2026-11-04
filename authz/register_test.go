package authz_test

import (
	"testing"

	"github.com/fitan/fxkit/authz"
)

func TestPoliciesFromRoutes(t *testing.T) {
	got := authz.PoliciesFromRoutes("admin", []authz.HTTPRoute{
		{Method: "GET", Path: "/users"},
		{Method: "GET", Path: "/users"}, // 去重
		{Method: "", Path: "/users/{id}"},
		{Method: "POST", Path: ""}, // 跳过
	})
	if len(got) != 2 {
		t.Fatalf("len=%d got=%+v", len(got), got)
	}
	if got[0].Sub != "admin" || got[0].Obj != "/users" || got[0].Act != "GET" {
		t.Fatalf("first=%+v", got[0])
	}
	if got[1].Obj != "/users/{id}" || got[1].Act != "*" {
		t.Fatalf("second=%+v", got[1])
	}
}
