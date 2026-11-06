package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/openapi"
	"github.com/fitan/fxkit/server"
)

func TestCollectOpenAPI_liveSpec(t *testing.T) {
	b, err := collectOpenAPI([]di.Option{
		config.Module,
		server.Module,
		server.ListenerModule,
		fxhuma.Module,
		fxhuma.ProvideRegistrar(fxhuma.FuncRegistrar(func(api huma.API) {
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
		})),
	}, config.Options{}, openapi.EncodeInput{Version: openapi.Spec30, Format: openapi.FormatYAML}, false)
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
	// 模拟 DB 或 Hatchet 在 OnStart 中失败、但 OpenAPI 仍可离线生成的环境。
	// 默认（bestEffort=false）会直接失败；输出部分 spec 必须显式选择 best-effort。
	opts := []di.Option{
		config.Module,
		server.Module,
		server.ListenerModule,
		fxhuma.Module,
		fxhuma.ProvideRegistrar(fxhuma.FuncRegistrar(func(api huma.API) {
			fxhuma.Register(api, huma.Operation{
				OperationID: "offline-endpoint",
				Method:      http.MethodGet,
				Path:        "/offline",
			}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
				return nil, nil
			})
		})),
		func(i di.Injector) {
			lc, _ := di.Invoke[*di.Lifecycle](i)
			if lc != nil {
				lc.Append(di.Hook{
					OnStart: func(_ context.Context) error {
						return errors.New("database connection refused (offline)")
					},
				})
			}
		},
	}

	in := openapi.EncodeInput{Version: openapi.Spec30, Format: openapi.FormatYAML}

	if _, err := collectOpenAPI(opts, config.Options{}, in, false); err == nil {
		t.Fatal("expected startup failure to abort spec generation by default")
	}

	b, err := collectOpenAPI(opts, config.Options{}, in, true)
	if err != nil {
		t.Fatalf("expected best-effort export to succeed, got %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "offline-endpoint") {
		t.Fatalf("missing operation in offline spec:\n%s", s)
	}
}

func TestCollectOpenAPI_requiresHuma(t *testing.T) {
	_, err := collectOpenAPI([]di.Option{
		config.Module,
		server.Module,
	}, config.Options{}, openapi.EncodeInput{}, false)
	if err == nil || !strings.Contains(err.Error(), "no Huma API") {
		t.Fatalf("got %v", err)
	}
}
