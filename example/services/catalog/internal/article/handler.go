package article

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/authz"
	fxhuma "github.com/fitan/fxkit/huma"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

// articleResource 用 fxhuma.RegisterResource 一次性生成 list/get/create/update/delete 五个 operation。
// Path 是资源根路径，{id} 与请求/响应类型由泛型参数推导。
var articleResource = fxhuma.RegisterResourceInput[UpdateArticleReq]{
	Path:        "/articles",
	Tags:        []string{"Articles"},
	ListSummary: "List articles (q= / cursor)",
	BindUpdate: func(id string, body UpdateArticleReq) UpdateArticleReq {
		body.ID = id
		return body
	},
}

// NewHumaRegistrar 注册文章 CRUD 与 fan-out 查询。
// Registrar 是懒加载的：启动装配完成后才拿到 Service。
func NewHumaRegistrar(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.RegisterResource(api, svc, articleResource)
		fxhuma.Register(api, fanoutOp, func(ctx context.Context, _ *struct{}) (*fanoutOut, error) {
			rows, err := svc.ListFanout(ctx)
			if err != nil {
				return nil, err
			}
			return &fanoutOut{Body: fanoutBody{Items: rows}}, nil
		})
	})
}

// fanoutBody/fanoutOut 是 /article-fanout 的响应包装（Huma 要求 Body 字段）。
type fanoutBody struct {
	// Items 是 fan-out 副表行。
	Items []entity.ArticleFanout `json:"items"`
}

// fanoutOut 是 /article-fanout 的响应包装。
type fanoutOut struct {
	Body fanoutBody
}

// fanoutOp 读取第二个 Hatchet 消费者写入的 fan-out 副表。
var fanoutOp = huma.Operation{
	OperationID: "listArticleFanout",
	Method:      http.MethodGet,
	Path:        "/article-fanout",
	Summary:     "Second Hatchet consumer of article-created (fan-out)",
	Tags:        []string{"Articles"},
}

// articleHTTPRoutes 把 Huma operation 转成 authz 路由目录项。
// 不登记的话，Casbin 开启时这些接口会被当成「未注册路由」拒绝。
func articleHTTPRoutes() []authz.HTTPRoute {
	ops := append(fxhuma.ResourceOperations(articleResource), fanoutOp)
	return authz.HTTPRoutesFromOperations(ops...)
}
