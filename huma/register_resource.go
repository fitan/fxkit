package huma

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/crudx"
)

// ResourceHandlers 是 [RegisterResource] 所需的可选 REST 接口。
// List 应使用 [crudx.List] 实现；写操作仍是普通的 service 方法。
type ResourceHandlers[ListRow, Detail, CreateReq, UpdateReq any] interface {
	List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[ListRow], error)
	Get(ctx context.Context, id string) (Detail, error)
	Create(ctx context.Context, req CreateReq) (Detail, error)
	Update(ctx context.Context, req UpdateReq) (Detail, error)
	Delete(ctx context.Context, id string) error
}

// RegisterResourceInput 配置路径前缀与 operation 元数据。
type RegisterResourceInput[UpdateReq any] struct {
	Path        string   // 例如 "/users"
	Tags        []string // OpenAPI 标签
	ListSummary string
	// BindUpdate 在调用 Update 之前将路径 id 注入 UpdateReq。
	// 若为 nil，则不注册 PUT。
	BindUpdate func(id string, body UpdateReq) UpdateReq

	// 可选的 operation 开关（默认 false = 启用）
	DisableList   bool // 为 true 时不注册 GET {path}
	DisableGet    bool // 为 true 时不注册 GET {path}/{id}
	DisableCreate bool // 为 true 时不注册 POST {path}
	DisableUpdate bool // 为 true 时不注册 PUT {path}/{id}，即使已设置 BindUpdate
	DisableDelete bool // 为 true 时不注册 DELETE {path}/{id}

	// PascalCaseOperationID 生成 PascalCase 形式的 operationId（例如 ListArticles / GetArticles）。
	// 默认 false 以保持与现有 spec 的向后兼容（listarticles）。
	PascalCaseOperationID bool
}

// ResourceOperations 返回 [RegisterResource] 挂载的 Huma operation。
// 若同时要发布 HTTPRoute，可配合 [authz.HTTPRoutesFromOperations] 使用；
// 启用 auto_register_routes 时，OpenAPI 抓取已覆盖这些路径。
func ResourceOperations[UpdateReq any](in RegisterResourceInput[UpdateReq]) []huma.Operation {
	path := in.Path
	tags := in.Tags
	listSummary := in.ListSummary
	if listSummary == "" {
		listSummary = "List"
	}
	opSuffix := sanitizeOpID(path, in.PascalCaseOperationID)

	var ops []huma.Operation
	if !in.DisableList {
		prefix := "list"
		if in.PascalCaseOperationID {
			prefix = "List"
		}
		ops = append(ops, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodGet,
			Path:        path,
			Summary:     listSummary,
			Tags:        tags,
		})
	}
	if !in.DisableGet {
		prefix := "get"
		if in.PascalCaseOperationID {
			prefix = "Get"
		}
		ops = append(ops, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodGet,
			Path:        path + "/{id}",
			Summary:     "Get",
			Tags:        tags,
		})
	}
	if !in.DisableCreate {
		prefix := "create"
		if in.PascalCaseOperationID {
			prefix = "Create"
		}
		ops = append(ops, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodPost,
			Path:        path,
			Summary:     "Create",
			Tags:        tags,
		})
	}
	if in.BindUpdate != nil && !in.DisableUpdate {
		prefix := "update"
		if in.PascalCaseOperationID {
			prefix = "Update"
		}
		ops = append(ops, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodPut,
			Path:        path + "/{id}",
			Summary:     "Update",
			Tags:        tags,
		})
	}
	if !in.DisableDelete {
		prefix := "delete"
		if in.PascalCaseOperationID {
			prefix = "Delete"
		}
		ops = append(ops, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodDelete,
			Path:        path + "/{id}",
			Summary:     "Delete",
			Tags:        tags,
		})
	}
	return ops
}

type idPath struct {
	ID string `path:"id"`
}

type listOut[T any] struct {
	Body crudx.ListResult[T]
}

type detailOut[T any] struct {
	Body T
}

type createIn[T any] struct {
	Body T
}

type updateIn[T any] struct {
	ID   string `path:"id"`
	Body T
}

type emptyOut struct{}

// RegisterResource 可选地为资源挂载五个 JSON 路由。
// 需要统一的 REST 形态时使用；仅提供列表的 API 用单个 [Register] 即可。
//
//	GET    {path}       列表（ZStack q=）
//	GET    {path}/{id}  获取
//	POST   {path}       创建
//	PUT    {path}/{id}  更新（需要 BindUpdate）
//	DELETE {path}/{id}  删除
//
// 面向共享 chi mux 上的 JSON/Huma 资源。
func RegisterResource[ListRow, Detail, CreateReq, UpdateReq any](
	api huma.API,
	svc ResourceHandlers[ListRow, Detail, CreateReq, UpdateReq],
	in RegisterResourceInput[UpdateReq],
) {
	path := in.Path
	tags := in.Tags
	listSummary := in.ListSummary
	if listSummary == "" {
		listSummary = "List"
	}
	opSuffix := sanitizeOpID(path, in.PascalCaseOperationID)

	if !in.DisableList {
		prefix := "list"
		if in.PascalCaseOperationID {
			prefix = "List"
		}
		Register(api, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodGet,
			Path:        path,
			Summary:     listSummary,
			Tags:        tags,
		}, func(ctx context.Context, input *struct {
			ListQueryInput
		}) (*listOut[ListRow], error) {
			params, err := ListParamsFromInput(&input.ListQueryInput)
			if err != nil {
				return nil, err
			}
			out, err := svc.List(ctx, params)
			if err != nil {
				return nil, err
			}
			return &listOut[ListRow]{Body: out}, nil
		})
	}

	if !in.DisableGet {
		prefix := "get"
		if in.PascalCaseOperationID {
			prefix = "Get"
		}
		Register(api, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodGet,
			Path:        path + "/{id}",
			Summary:     "Get",
			Tags:        tags,
		}, func(ctx context.Context, input *idPath) (*detailOut[Detail], error) {
			out, err := svc.Get(ctx, input.ID)
			if err != nil {
				return nil, err
			}
			return &detailOut[Detail]{Body: out}, nil
		})
	}

	if !in.DisableCreate {
		prefix := "create"
		if in.PascalCaseOperationID {
			prefix = "Create"
		}
		Register(api, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodPost,
			Path:        path,
			Summary:     "Create",
			Tags:        tags,
		}, func(ctx context.Context, input *createIn[CreateReq]) (*detailOut[Detail], error) {
			out, err := svc.Create(ctx, input.Body)
			if err != nil {
				return nil, err
			}
			return &detailOut[Detail]{Body: out}, nil
		})
	}

	if in.BindUpdate != nil && !in.DisableUpdate {
		prefix := "update"
		if in.PascalCaseOperationID {
			prefix = "Update"
		}
		Register(api, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodPut,
			Path:        path + "/{id}",
			Summary:     "Update",
			Tags:        tags,
		}, func(ctx context.Context, input *updateIn[UpdateReq]) (*detailOut[Detail], error) {
			req := in.BindUpdate(input.ID, input.Body)
			out, err := svc.Update(ctx, req)
			if err != nil {
				return nil, err
			}
			return &detailOut[Detail]{Body: out}, nil
		})
	}

	if !in.DisableDelete {
		prefix := "delete"
		if in.PascalCaseOperationID {
			prefix = "Delete"
		}
		Register(api, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodDelete,
			Path:        path + "/{id}",
			Summary:     "Delete",
			Tags:        tags,
		}, func(ctx context.Context, input *idPath) (*emptyOut, error) {
			if err := svc.Delete(ctx, input.ID); err != nil {
				return nil, err
			}
			return &emptyOut{}, nil
		})
	}
}

func sanitizeOpID(path string, pascalCase bool) string {
	if !pascalCase {
		var b strings.Builder
		for _, r := range path {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				b.WriteRune(r)
			}
		}
		return b.String()
	}

	// PascalCase 转换（例如 "/order-items" -> "OrderItems"）
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '-' || r == '_'
	})
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}
