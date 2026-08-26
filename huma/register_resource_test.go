package huma

import (
	"net/http"
	"testing"
)

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
