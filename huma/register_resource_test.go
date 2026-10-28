package huma

import (
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/fitan/fxkit/crudx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type dummyCreate struct {
	Title string `json:"title"`
}

type dummyUpdate struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type dummyService struct{}

func (dummyService) List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[dummyItem], error) {
	return crudx.ListResult[dummyItem]{Items: []dummyItem{{ID: "1", Title: "first"}}}, nil
}

func (dummyService) Get(ctx context.Context, id string) (dummyItem, error) {
	return dummyItem{ID: id, Title: "found"}, nil
}

func (dummyService) Create(ctx context.Context, req dummyCreate) (dummyItem, error) {
	return dummyItem{ID: "new", Title: req.Title}, nil
}

func (dummyService) Update(ctx context.Context, req dummyUpdate) (dummyItem, error) {
	return dummyItem{ID: req.ID, Title: req.Title}, nil
}

func (dummyService) Delete(ctx context.Context, id string) error {
	return nil
}

func TestRegisterResource_DirectCall(t *testing.T) {
	_, api := humatest.New(t)
	svc := &dummyService{}

	// 直接调用 RegisterResource，Go 编译器自动反向推导全部 4 个泛型参数
	RegisterResource(api, svc, RegisterResourceInput[dummyUpdate]{
		Path:        "/dummies",
		Tags:        []string{"Dummy"},
		ListSummary: "List dummies",
		BindUpdate: func(id string, body dummyUpdate) dummyUpdate {
			body.ID = id
			return body
		},
	})

	// 验证 OpenAPI 路由中是否包含了挂载的 5 个方法
	openAPI := api.OpenAPI()
	require.NotNil(t, openAPI.Paths["/dummies"])
	assert.NotNil(t, openAPI.Paths["/dummies"].Get)
	assert.NotNil(t, openAPI.Paths["/dummies"].Post)

	require.NotNil(t, openAPI.Paths["/dummies/{id}"])
	assert.NotNil(t, openAPI.Paths["/dummies/{id}"].Get)
	assert.NotNil(t, openAPI.Paths["/dummies/{id}"].Put)
	assert.NotNil(t, openAPI.Paths["/dummies/{id}"].Delete)
}

func TestResourceOperations(t *testing.T) {
	ops := ResourceOperations(RegisterResourceInput[struct{}]{
		Path:        "/articles",
		Tags:        []string{"Articles"},
		ListSummary: "List articles",
		BindUpdate: func(id string, body struct{}) struct{} {
			return body
		},
	})
	if len(ops) != 5 {
		t.Fatalf("len=%d", len(ops))
	}
	want := map[string]string{
		http.MethodGet + " /articles":         "listarticles",
		http.MethodGet + " /articles/{id}":    "getarticles",
		http.MethodPost + " /articles":        "createarticles",
		http.MethodPut + " /articles/{id}":    "updatearticles",
		http.MethodDelete + " /articles/{id}": "deletearticles",
	}
	for _, op := range ops {
		key := op.Method + " " + op.Path
		id, ok := want[key]
		if !ok {
			t.Fatalf("unexpected op %s", key)
		}
		if op.OperationID != id {
			t.Fatalf("%s OperationID=%q want %q", key, op.OperationID, id)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing %v", want)
	}

	noPut := ResourceOperations(RegisterResourceInput[struct{}]{Path: "/articles"})
	for _, op := range noPut {
		if op.Method == http.MethodPut {
			t.Fatal("PUT should be omitted without BindUpdate")
		}
	}
	if len(noPut) != 4 {
		t.Fatalf("len=%d want 4", len(noPut))
	}
}

func TestResourceOperations_TogglesAndPascalCase(t *testing.T) {
	// Test disabling specific operations and PascalCase OperationIDs
	ops := ResourceOperations(RegisterResourceInput[struct{}]{
		Path:                  "/order-items",
		Tags:                  []string{"Orders"},
		DisableDelete:         true,
		DisableCreate:         true,
		PascalCaseOperationID: true,
		BindUpdate: func(id string, body struct{}) struct{} {
			return body
		},
	})

	if len(ops) != 3 {
		t.Fatalf("len=%d want 3 (list, get, update)", len(ops))
	}

	want := map[string]string{
		http.MethodGet + " /order-items":      "ListOrderItems",
		http.MethodGet + " /order-items/{id}": "GetOrderItems",
		http.MethodPut + " /order-items/{id}": "UpdateOrderItems",
	}

	for _, op := range ops {
		key := op.Method + " " + op.Path
		expectedID, ok := want[key]
		if !ok {
			t.Fatalf("unexpected op %s", key)
		}
		if op.OperationID != expectedID {
			t.Fatalf("%s OperationID=%q want %q", key, op.OperationID, expectedID)
		}
	}
}
