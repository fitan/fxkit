package comment

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/crudx"
	fxhuma "github.com/fitan/fxkit/huma"
)

// listInput 复用 fxhuma.ListQueryInput，统一解析 q= / cursor / limit 等查询参数。
type listInput struct {
	fxhuma.ListQueryInput
}

// listOutput 直接复用 crudx 的列表结果（items + total/cursor）。
type listOutput struct {
	Body crudx.ListResult[CommentListRow]
}

// createInput 是创建评论的请求包装。
type createInput struct {
	Body CreateCommentReq
}

// createOutput 返回列表投影，与 /comments 列表行保持一致。
type createOutput struct {
	Body CommentListRow
}

// idPath 是带 {id} 路径参数的输入。
type idPath struct {
	// ID 是评论 ID。
	ID string `path:"id"`
}

// Register 注册评论的 list/create/delete 三个 Huma operation。
func Register(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.Register(api, listOp, func(ctx context.Context, in *listInput) (*listOutput, error) {
			params, err := fxhuma.ListParamsFromInput(&in.ListQueryInput)
			if err != nil {
				return nil, err
			}
			out, err := svc.List(ctx, params)
			if err != nil {
				return nil, err
			}
			return &listOutput{Body: out}, nil
		})
		fxhuma.Register(api, createOp, func(ctx context.Context, in *createInput) (*createOutput, error) {
			row, err := svc.Create(ctx, in.Body)
			if err != nil {
				return nil, err
			}
			return &createOutput{Body: row}, nil
		})
		fxhuma.Register(api, deleteOp, func(ctx context.Context, in *idPath) (*struct{}, error) {
			if err := svc.Delete(ctx, in.ID); err != nil {
				return nil, err
			}
			return &struct{}{}, nil
		})
	})
}

// listOp 的过滤字段包含 article.title，演示 crudx 的跨表 JOIN。
var (
	listOp = huma.Operation{
		OperationID: "listComments",
		Method:      "GET",
		Path:        "/comments",
		Summary:     "List comments (q= / cursor / JOIN article.title)",
		Tags:        []string{"Comments"},
	}
	createOp = huma.Operation{
		OperationID: "createComment",
		Method:      "POST",
		Path:        "/comments",
		Summary:     "Create a comment",
		Tags:        []string{"Comments"},
	}
	deleteOp = huma.Operation{
		OperationID: "deleteComment",
		Method:      "DELETE",
		Path:        "/comments/{id}",
		Summary:     "Soft-delete a comment",
		Tags:        []string{"Comments"},
	}
)

// commentHTTPRoutes 把评论 operation 登记到 authz 路由目录。
func commentHTTPRoutes() []authz.HTTPRoute {
	return authz.HTTPRoutesFromOperations(listOp, createOp, deleteOp)
}
