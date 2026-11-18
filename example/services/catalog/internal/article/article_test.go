package article_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/fitan/fxkit-example/services/catalog/internal/article"
	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/outbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDB 为每个测试创建独立的 SQLite 数据库。
func setupTestDB(t *testing.T) *gormx.Client {
	dsn := filepath.Join(t.TempDir(), "article_test.db")
	client, err := gormx.New(&gormx.Config{
		Driver:   "sqlite",
		DSN:      dsn,
		LogLevel: "silent",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	return client
}

// setupArticleService 构造 article.Service 并跑 Lifecycle.OnStart（迁移表）。
func setupArticleService(t *testing.T, client *gormx.Client) *article.Service {
	lc := di.NewLifecycle()
	obcfg := &outbox.Config{Enabled: false}

	svc, err := article.NewService(client, repository.NewArticleRepo(client), nil, nil, obcfg, lc)
	require.NoError(t, err)
	require.NotNil(t, svc)

	require.NoError(t, lc.Start(context.Background()))
	return svc
}

// TestArticleService_CRUD 覆盖创建/重名冲突/校验/查询/乐观锁更新/删除。
func TestArticleService_CRUD(t *testing.T) {
	ctx := context.Background()
	client := setupTestDB(t)
	svc := setupArticleService(t, client)

	// 1. 创建 Article
	created, err := svc.Create(ctx, article.CreateArticleReq{
		Title: "Go 1.27 Generics",
		Body:  "Deep dive into reverse type inference",
	})
	require.NoError(t, err)
	assert.True(t, created.ID > 0)
	assert.Equal(t, "Go 1.27 Generics", created.Title)
	assert.Equal(t, "Deep dive into reverse type inference", created.Body)
	assert.Equal(t, "draft", created.Status)
	assert.Equal(t, int64(1), created.Version)

	idStr := fmt.Sprintf("%d", created.ID)

	// 2. 重复标题应冲突
	_, err = svc.Create(ctx, article.CreateArticleReq{
		Title: "Go 1.27 Generics",
		Body:  "Another duplicate",
	})
	assert.Error(t, err)

	// 3. 空标题应校验失败
	_, err = svc.Create(ctx, article.CreateArticleReq{
		Title: "   ",
	})
	assert.Error(t, err)

	// 4. 按 ID 查询
	detail, err := svc.Get(ctx, idStr)
	require.NoError(t, err)
	assert.Equal(t, created.ID, detail.ID)
	assert.Equal(t, "Go 1.27 Generics", detail.Title)

	// 5. 更新
	updated, err := svc.Update(ctx, article.UpdateArticleReq{
		ID:      idStr,
		Version: detail.Version,
		Title:   "Go 1.27 Generics Updated",
		Body:    "Updated body content",
	})
	require.NoError(t, err)
	assert.Equal(t, "Go 1.27 Generics Updated", updated.Title)
	assert.Equal(t, "Updated body content", updated.Body)
	assert.Equal(t, detail.Version+1, updated.Version)

	// 陈旧 version 应该冲突
	_, err = svc.Update(ctx, article.UpdateArticleReq{
		ID:      idStr,
		Version: detail.Version,
		Title:   "stale update",
	})
	assert.Error(t, err)

	// 6. 带过滤条件的列表查询
	listRes, err := svc.List(ctx, crudx.ListParams{
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, len(listRes.Items))
	assert.Equal(t, "Go 1.27 Generics Updated", listRes.Items[0].Title)

	// 7. 删除
	err = svc.Delete(ctx, idStr)
	require.NoError(t, err)

	// 8. 删除后再查询应失败
	_, err = svc.Get(ctx, idStr)
	assert.Error(t, err)
}

// TestArticleService_Fanout 验证 fan-out 副表初始为空。
func TestArticleService_Fanout(t *testing.T) {
	ctx := context.Background()
	client := setupTestDB(t)
	svc := setupArticleService(t, client)

	// 测试 fan-out 列表初始为空
	rows, err := svc.ListFanout(ctx)
	require.NoError(t, err)
	assert.Empty(t, rows)
}
