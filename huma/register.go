package huma

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

// Register 类似 [huma.Register]，但用 [AsError] 转换返回的 error。
//
// OperationID 重复会让 Huma 在构建 spec 时 panic（"duplicate operation ID"）。
// 这里提前检测并 panic，错误信息带上 method/path，便于启动时直接修正；
// 不再自动改名（改名会让已生成的 typed client 与运行时契约漂移）。
func Register[I, O any](api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	if op.OperationID != "" && api != nil && api.OpenAPI() != nil &&
		operationIDTaken(api.OpenAPI(), op.OperationID) {
		panic(fmt.Sprintf("huma: duplicate operation ID %q (method=%s path=%s); set an explicit unique OperationID",
			op.OperationID, op.Method, op.Path))
	}
	huma.Register(api, op, func(ctx context.Context, input *I) (*O, error) {
		out, err := handler(ctx, input)
		return out, AsError(err)
	})
}

// operationIDTaken 报告 OpenAPI 文档中是否已存在该 OperationID。
func operationIDTaken(doc *huma.OpenAPI, id string) bool {
	if doc == nil || id == "" {
		return false
	}
	for _, item := range doc.Paths {
		if item == nil {
			continue
		}
		for _, op := range []*huma.Operation{
			item.Get, item.Put, item.Post, item.Delete,
			item.Options, item.Head, item.Patch, item.Trace,
		} {
			if op != nil && op.OperationID == id {
				return true
			}
		}
	}
	return false
}
