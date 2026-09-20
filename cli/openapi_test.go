package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/openapi"
	"github.com/fitan/fxkit/server"
	"go.uber.org/fx"
)

func TestCollectOpenAPI_liveSpec(t *testing.T) {
	b, err := collectOpenAPI([]fx.Option{
		config.Module,
		server.Module,
		server.ListenerModule,
		fxhuma.Module,
		fxhuma.ProvideRegistrar(func() fxhuma.Registrar {
			return fxhuma.FuncRegistrar(func(api huma.API) {
				fxhuma.Register(api, huma.Operation{
					OperationID: "get-greeting",
					Method:      http.MethodGet,
					Path:        "/greeting/{name}",
				}, func(_ context.Context, in *struct {
					Name string `path:"name"`
				}) (*struct {
					Body struct {
						Message string `json:"message"`
					}
				}, error) {
					return nil, nil
				})
			})
		}),
	}, config.Options{}, openapi.EncodeInput{Version: openapi.Spec30, Format: openapi.FormatYAML})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "3.0.3") {
		t.Fatalf("want 3.0.3:\n%s", s)
	}
	if !strings.Contains(s, "get-greeting") {
		t.Fatalf("missing operation:\n%s", s)
	}
}

func TestCollectOpenAPI_OfflineWithFailingHook(t *testing.T) {
	// Simulate an environment where DB or Hatchet fails in OnStart, but OpenAPI can still be generated offline
	b, err := collectOpenAPI([]fx.Option{
		config.Module,
		server.Module,
		server.ListenerModule,
		fxhuma.Module,
		fxhuma.ProvideRegistrar(func() fxhuma.Registrar {
			return fxhuma.FuncRegistrar(func(api huma.API) {
				fxhuma.Register(api, huma.Operation{
					OperationID: "offline-endpoint",
					Method:      http.MethodGet,
					Path:        "/offline",
				}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
					return nil, nil
				})
			})
		}),
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{
				OnStart: func(_ context.Context) error {
					return errors.New("database connection refused (offline)")
				},
			})
		}),
	}, config.Options{}, openapi.EncodeInput{Version: openapi.Spec30, Format: openapi.FormatYAML})
	if err != nil {
		t.Fatalf("expected offline export to succeed, got %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "offline-endpoint") {
		t.Fatalf("missing operation in offline spec:\n%s", s)
	}
}

func TestCollectOpenAPI_requiresHuma(t *testing.T) {
	_, err := collectOpenAPI([]fx.Option{
		config.Module,
		server.Module,
	}, config.Options{}, openapi.EncodeInput{})
	if err == nil || !strings.Contains(err.Error(), "no Huma API") {
		t.Fatalf("got %v", err)
	}
}
