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
	// List 返回受控列表；应使用 [crudx.List] 以支持 q=、游标与分页上限。
	List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[ListRow], error)
	// Get 按 id 返回详情；不存在时返回 fxerrors.NotFound。
	Get(ctx context.Context, id string) (Detail, error)
	// Create 创建资源并返回详情。
	Create(ctx context.Context, req CreateReq) (Detail, error)
	// Update 更新资源并返回详情。
	Update(ctx context.Context, req UpdateReq) (Detail, error)
	// Delete 按 id 删除资源；不存在时返回 fxerrors.NotFound。
	Delete(ctx context.Context, id string) error
}

// RegisterResourceInput 配置路径前缀与 operation 元数据。
type RegisterResourceInput[UpdateReq any] struct {
	Path string   // 例如 "/users"
	Tags []string // OpenAPI 标签
	// ListSummary 是列表 operation 的 summary；为空时默认为 "List"。
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

// resourceOpKind 标识一条资源路由的语义（用于绑定对应 handler）。
type resourceOpKind int

const (
	// opList 对应 GET {path} 列表路由。
	opList resourceOpKind = iota
	// opGet 对应 GET {path}/{id} 详情路由。
	opGet
	// opCreate 对应 POST {path} 创建路由。
	opCreate
	// opUpdate 对应 PUT {path}/{id} 更新路由。
	opUpdate
	// opDelete 对应 DELETE {path}/{id} 删除路由。
	opDelete
)

// resourceOp 是一条资源路由的元数据 + 语义。
type resourceOp struct {
	// kind 是路由语义，决定 [RegisterResource] 绑定哪个 handler。
	kind resourceOpKind
	// op 是传给 Huma 的 operation 元数据（id/method/path/summary/tags）。
	op huma.Operation
}

// buildResourceOps 是 operationId / method / path / summary / tags 的**唯一**构造点。
//
// [ResourceOperations]（给 authz 声明权限）与 [RegisterResource]（真正挂载）都从这里取，
// 避免两份逐字复制：早期实现各写一遍，漏改一侧就会出现「注册的 op 与 authz 声明的 op
// 不一致」——鉴权放行但接口不存在，或反之。
func buildResourceOps[UpdateReq any](in RegisterResourceInput[UpdateReq]) []resourceOp {
	path := in.Path
	tags := in.Tags
	listSummary := in.ListSummary
	if listSummary == "" {
		listSummary = "List"
	}
	opSuffix := sanitizeOpID(path, in.PascalCaseOperationID)
	prefix := func(lower, upper string) string {
		if in.PascalCaseOperationID {
			return upper
		}
		return lower
	}

	var ops []resourceOp
	if !in.DisableList {
		ops = append(ops, resourceOp{opList, huma.Operation{
			OperationID: prefix("list", "List") + opSuffix,
			Method:      http.MethodGet,
			Path:        path,
			Summary:     listSummary,
			Tags:        tags,
		}})
	}
	if !in.DisableGet {
		ops = append(ops, resourceOp{opGet, huma.Operation{
			OperationID: prefix("get", "Get") + opSuffix,
			Method:      http.MethodGet,
			Path:        path + "/{id}",
			Summary:     "Get",
			Tags:        tags,
		}})
	}
	if !in.DisableCreate {
		ops = append(ops, resourceOp{opCreate, huma.Operation{
			OperationID: prefix("create", "Create") + opSuffix,
			Method:      http.MethodPost,
			Path:        path,
			Summary:     "Create",
			Tags:        tags,
		}})
	}
	if in.BindUpdate != nil && !in.DisableUpdate {
		ops = append(ops, resourceOp{opUpdate, huma.Operation{
			OperationID: prefix("update", "Update") + opSuffix,
			Method:      http.MethodPut,
			Path:        path + "/{id}",
			Summary:     "Update",
			Tags:        tags,
		}})
	}
	if !in.DisableDelete {
		ops = append(ops, resourceOp{opDelete, huma.Operation{
			OperationID: prefix("delete", "Delete") + opSuffix,
			Method:      http.MethodDelete,
			Path:        path + "/{id}",
			Summary:     "Delete",
			Tags:        tags,
		}})
	}
	return ops
}

// ResourceOperations 返回 [RegisterResource] 挂载的 Huma operation。
// 若同时要发布 HTTPRoute，可配合 [authz.HTTPRoutesFromOperations] 使用；
// 启用 auto_register_routes 时，OpenAPI 抓取已覆盖这些路径。
func ResourceOperations[UpdateReq any](in RegisterResourceInput[UpdateReq]) []huma.Operation {
	ops := buildResourceOps(in)
	out := make([]huma.Operation, len(ops))
	for i, o := range ops {
		out[i] = o.op
	}
	return out
}

// idPath 是只带路径参数的请求输入，对应 URL 中的 {id}。
type idPath struct {
	// ID 是路径参数 id。
	ID string `path:"id"`
}

// listOut 是列表路由的响应，Body 直接作为 JSON 响应体。
type listOut[T any] struct {
	// Body 序列化为 items/start/limit（以及可选的 total、nextCursor）字段。
	Body crudx.ListResult[T]
}

// detailOut 是详情类路由的响应，Body 直接作为 JSON 响应体。
type detailOut[T any] struct {
	// Body 序列化为资源详情对象的 JSON。
	Body T
}

// createIn 是创建路由的请求输入。
type createIn[T any] struct {
	// Body 是请求 JSON 解析出的创建 DTO。
	Body T
}

// updateIn 是更新路由的请求输入：路径 id 加请求体。
type updateIn[T any] struct {
	// ID 是路径参数 id，传给 [RegisterResourceInput.BindUpdate]。
	ID string `path:"id"`
	// Body 是请求 JSON 解析出的更新 DTO。
	Body T
}

// emptyOut 是空输出：没有 Body 字段，Huma 返回 204 No Content。
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
	for _, ro := range buildResourceOps(in) {
		switch ro.kind {
		case opList:
			Register(api, ro.op, func(ctx context.Context, input *struct {
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
		case opGet:
			Register(api, ro.op, func(ctx context.Context, input *idPath) (*detailOut[Detail], error) {
				out, err := svc.Get(ctx, input.ID)
				if err != nil {
					return nil, err
				}
				return &detailOut[Detail]{Body: out}, nil
			})
		case opCreate:
			Register(api, ro.op, func(ctx context.Context, input *createIn[CreateReq]) (*detailOut[Detail], error) {
				out, err := svc.Create(ctx, input.Body)
				if err != nil {
					return nil, err
				}
				return &detailOut[Detail]{Body: out}, nil
			})
		case opUpdate:
			Register(api, ro.op, func(ctx context.Context, input *updateIn[UpdateReq]) (*detailOut[Detail], error) {
				req := in.BindUpdate(input.ID, input.Body)
				out, err := svc.Update(ctx, req)
				if err != nil {
					return nil, err
				}
				return &detailOut[Detail]{Body: out}, nil
			})
		case opDelete:
			Register(api, ro.op, func(ctx context.Context, input *idPath) (*emptyOut, error) {
				if err := svc.Delete(ctx, input.ID); err != nil {
					return nil, err
				}
				return &emptyOut{}, nil
			})
		}
	}
}

// sanitizeOpID 从 path 生成 OperationID：pascalCase 为真时按 / - _ 切分并首字母大写，否则只保留字母数字。
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
