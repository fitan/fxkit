package digest

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/authz"
	fxhuma "github.com/fitan/fxkit/huma"
)

// Register 注册 /digest：storefront 通过 reqx 调 catalog 后聚合返回。
func Register(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.Register(api, huma.Operation{
			OperationID: "getDigest",
			Method:      http.MethodGet,
			Path:        "/digest",
			Summary:     "Aggregate articles from catalog via reqx gen client",
			Tags:        []string{"Storefront"},
		}, svc.Digest)
	})
}

// digestHTTPRoutes 把 /digest 登记到 authz 目录。
func digestHTTPRoutes() []authz.HTTPRoute {
	return authz.HTTPRoutesFromOperations(huma.Operation{
		OperationID: "getDigest",
		Method:      http.MethodGet,
		Path:        "/digest",
		Summary:     "Aggregate articles from catalog via reqx gen client",
		Tags:        []string{"Storefront"},
	})
}
