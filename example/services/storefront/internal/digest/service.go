package digest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/reqx"

	catalogclient "github.com/fitan/fxkit-example/services/storefront/internal/clients/catalog"
)

// Config 是 storefront 段配置：catalog 的 Consul 服务名与本地直连种子。
type Config struct {
	// CatalogName 是 catalog 的 Consul 服务名。
	CatalogName string `yaml:"catalog_name"`
	// CatalogSeed 是本地直连地址（无 Consul 时使用）。
	CatalogSeed string `yaml:"catalog_seed"`
}

// SetDefaults 提供本地开发的默认值（直连 127.0.0.1:8081）。
func (c *Config) SetDefaults() {
	c.CatalogName = "catalog"
	c.CatalogSeed = "127.0.0.1:8081"
}

// Service 聚合 catalog 的文章数据。
// sdk 是生成的 typed client；factory 用于展示当前解析到的 endpoints。
type Service struct {
	sdk     *catalogclient.ClientWithResponses
	factory *reqx.Factory
	cfg     *Config
	input   reqx.ClientInput
}

// NewService 用 reqx factory 构建 catalog 客户端；有 seed 时优先直连，便于本地调试。
func NewService(factory *reqx.Factory, cfg *Config) (*Service, error) {
	if factory == nil {
		return nil, fxerrors.Internal("storefront: nil reqx factory")
	}
	if cfg == nil {
		cfg = &Config{}
		cfg.SetDefaults()
	}
	in := reqx.ClientInput{Name: cfg.CatalogName}
	if cfg.CatalogSeed != "" {
		in.Seeds = []string{cfg.CatalogSeed}
	}
	sdk, err := catalogclient.NewFromFactory(factory, in)
	if err != nil {
		return nil, err
	}
	return &Service{sdk: sdk, factory: factory, cfg: cfg, input: in}, nil
}

// withUser 把入站请求的 authz subject 透传给 catalog（下游仍会再鉴权一次）。
func withUser(ctx context.Context) catalogclient.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		if subj, ok := authz.SubjectFromContext(ctx); ok && subj.ID != "" {
			req.Header.Set("X-User", subj.ID)
		}
		return nil
	}
}

// Digest 调 catalog 列表接口并原样返回 JSON。
// limit 夹在 [1,100]，上游非 2xx 映射成对应的 fxerrors。
func (s *Service) Digest(ctx context.Context, in *DigestInput) (*DigestOutput, error) {
	limit := int64(in.Limit)
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	sortBy := "id"
	dir := catalogclient.ListarticlesParamsSortDirection("desc")
	reply := true
	resp, err := s.sdk.ListarticlesWithResponse(ctx, &catalogclient.ListarticlesParams{
		Limit:          &limit,
		SortBy:         &sortBy,
		SortDirection:  &dir,
		ReplyWithCount: &reply,
	}, withUser(ctx))
	if err != nil {
		return nil, fxerrors.Unavailable("catalog: %v", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, mapCatalogStatus(resp.StatusCode(), string(resp.Body))
	}
	articles := json.RawMessage(resp.Body)
	if !json.Valid(articles) {
		articles = json.RawMessage(`{"raw":true}`)
	}
	return &DigestOutput{Body: DigestBody{
		Source:    s.cfg.CatalogName,
		Via:       "reqx.NewFromFactory",
		Endpoints: s.factory.Endpoints(s.input),
		Articles:  articles,
	}}, nil
}

// mapCatalogStatus 把上游 HTTP 状态映射为 fxerrors，保留 401/403/404 语义。
func mapCatalogStatus(code int, body string) error {
	switch code {
	case http.StatusUnauthorized:
		return fxerrors.Unauthorized("catalog unauthorized")
	case http.StatusForbidden:
		return fxerrors.PermissionDenied("catalog forbidden")
	case http.StatusNotFound:
		return fxerrors.NotFound("catalog", "%s", body)
	default:
		return fxerrors.Unavailable("catalog status=%d", code)
	}
}
