package digest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/fitan/fxkit-example/services/storefront/internal/digest"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/reqx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDigestService_Success(t *testing.T) {
	// 1. 启动一个模拟的 Catalog 服务 HTTP Server
	mockCatalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/articles", r.URL.Path)
		assert.Equal(t, "alice", r.Header.Get("X-User"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 1,
			"items": []map[string]any{
				{"id": 1, "title": "Mock Article", "status": "published"},
			},
		})
	}))
	defer mockCatalog.Close()

	// 2. 初始化 reqx.Factory 指向 mockCatalog 地址
	factory := reqx.NewFactory(nil, nil)
	cfg := &digest.Config{
		CatalogName: "catalog",
		CatalogSeed: mockCatalog.Listener.Addr().String(),
	}

	svc, err := digest.NewService(factory, cfg)
	require.NoError(t, err)
	require.NotNil(t, svc)

	// 3. 在 context 中放入 alice 的 Subject，验证透传
	ctx := authz.WithSubject(context.Background(), authz.Subject{ID: "alice"})

	out, err := svc.Digest(ctx, &digest.DigestInput{Limit: 5})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, "catalog", out.Body.Source)
	assert.Equal(t, "reqx.NewFromFactory", out.Body.Via)
	assert.Contains(t, string(out.Body.Articles), "Mock Article")
}

func TestDigestService_CatalogErrors(t *testing.T) {
	// 启动返回 403 Forbidden 的模拟服务
	mockCatalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("permission denied"))
	}))
	defer mockCatalog.Close()

	factory := reqx.NewFactory(nil, nil)
	cfg := &digest.Config{
		CatalogName: "catalog",
		CatalogSeed: mockCatalog.Listener.Addr().String(),
	}

	svc, err := digest.NewService(factory, cfg)
	require.NoError(t, err)

	_, err = svc.Digest(context.Background(), &digest.DigestInput{Limit: 10})
	assert.Error(t, err)
}

func TestDigest_HumaRegistration(t *testing.T) {
	mockCatalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer mockCatalog.Close()

	factory := reqx.NewFactory(nil, nil)
	cfg := &digest.Config{
		CatalogName: "catalog",
		CatalogSeed: mockCatalog.Listener.Addr().String(),
	}
	svc, err := digest.NewService(factory, cfg)
	require.NoError(t, err)

	_, api := humatest.New(t)
	digest.Register(svc).Register(api)

	resp := api.Get("/digest")
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"via":"reqx.NewFromFactory"`)
}
