package digest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/fxerrors"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/reqx"
	"go.uber.org/fx"

	catalogclient "github.com/fitan/fxkit-example/services/storefront/internal/clients/catalog"
)

type Config struct {
	CatalogName string `yaml:"catalog_name"`
	CatalogSeed string `yaml:"catalog_seed"`
}

func (c *Config) SetDefaults() {
	c.CatalogName = "catalog"
	c.CatalogSeed = "127.0.0.1:8081"
}

type Service struct {
	sdk     *catalogclient.ClientWithResponses
	factory *reqx.Factory
	cfg     *Config
	input   reqx.ClientInput
}

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

type DigestInput struct {
	Limit int `query:"limit" default:"10"`
}

type DigestOutput struct {
	Body DigestBody
}

type DigestBody struct {
	Source    string          `json:"source"`
	Via       string          `json:"via"`
	Endpoints []string        `json:"endpoints"`
	Articles  json.RawMessage `json:"articles"`
}

func withUser(ctx context.Context) catalogclient.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		if subj, ok := authz.SubjectFromContext(ctx); ok && subj.ID != "" {
			req.Header.Set("X-User", subj.ID)
		}
		return nil
	}
}

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

func digestHTTPRoutes() []authz.HTTPRoute {
	return authz.HTTPRoutesFromOperations(huma.Operation{
		OperationID: "getDigest",
		Method:      http.MethodGet,
		Path:        "/digest",
		Summary:     "Aggregate articles from catalog via reqx gen client",
		Tags:        []string{"Storefront"},
	})
}

var Module = fx.Options(
	fxkit.Service("digest", NewService,
		fxkit.ProvideConfig[Config]("storefront"),
	),
	fxhuma.ProvideRegistrar(Register),
	authz.ProvideHTTPRoutes(digestHTTPRoutes),
)
