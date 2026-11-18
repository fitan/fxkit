package huma

import (
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/go-chi/chi/v5"
)

type dupIn struct{}

type dupOut struct {
	Body struct{}
}

// TestRegisterPanicsOnDuplicateOperationIDs 回归：重复 operationId 必须启动失败，
// 不再自动改名（改名会让已生成的 client 与运行时契约漂移）。
func TestRegisterPanicsOnDuplicateOperationIDs(t *testing.T) {
	mux := chi.NewRouter()
	api := NewAPI(mux, &config.App{Name: "t"})
	h := func(context.Context, *dupIn) (*dupOut, error) { return &dupOut{}, nil }

	Register(api, huma.Operation{OperationID: "dup", Method: http.MethodGet, Path: "/a"}, h)
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate operation ID must panic at startup")
		}
	}()
	Register(api, huma.Operation{OperationID: "dup", Method: http.MethodGet, Path: "/b"}, h)
}
