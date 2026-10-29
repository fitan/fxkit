package article_test

import (
	"context"
	"fmt"
	"testing"
	"path/filepath"

	"github.com/fitan/fxkit-example/services/catalog/internal/article"
	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/outbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func setupArticleService(t *testing.T, client *gormx.Client) *article.Service {
	lc := di.NewLifecycle()
	obcfg := &outbox.Config{Enabled: false}

	svc, err := article.NewService(client, nil, nil, obcfg, lc)
	require.NoError(t, err)
	require.NotNil(t, svc)

	require.NoError(t, lc.Start(context.Background()))
	return svc
}

func TestArticleService_CRUD(t *testing.T) {
	ctx := context.Background()
	client := setupTestDB(t)
	svc := setupArticleService(t, client)

	// 1. Create Article
	created, err := svc.Create(ctx, article.CreateArticleReq{
		Title: "Go 1.27 Generics",
		Body:  "Deep dive into reverse type inference",
	})
	require.NoError(t, err)
	assert.True(t, created.ID > 0)
	assert.Equal(t, "Go 1.27 Generics", created.Title)
	assert.Equal(t, "Deep dive into reverse type inference", created.Body)
	assert.Equal(t, "draft", created.Status)

	idStr := fmt.Sprintf("%d", created.ID)

	// 2. Conflict on Duplicate Title
	_, err = svc.Create(ctx, article.CreateArticleReq{
		Title: "Go 1.27 Generics",
		Body:  "Another duplicate",
	})
	assert.Error(t, err)

	// 3. Validation on Empty Title
	_, err = svc.Create(ctx, article.CreateArticleReq{
		Title: "   ",
	})
	assert.Error(t, err)

	// 4. Get by ID
	detail, err := svc.Get(ctx, idStr)
	require.NoError(t, err)
	assert.Equal(t, created.ID, detail.ID)
	assert.Equal(t, "Go 1.27 Generics", detail.Title)

	// 5. Update
	updated, err := svc.Update(ctx, article.UpdateArticleReq{
		ID:    idStr,
		Title: "Go 1.27 Generics Updated",
		Body:  "Updated body content",
	})
	require.NoError(t, err)
	assert.Equal(t, "Go 1.27 Generics Updated", updated.Title)
	assert.Equal(t, "Updated body content", updated.Body)

	// 6. List with filter
	listRes, err := svc.List(ctx, crudx.ListParams{
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, len(listRes.Items))
	assert.Equal(t, "Go 1.27 Generics Updated", listRes.Items[0].Title)

	// 7. Delete
	err = svc.Delete(ctx, idStr)
	require.NoError(t, err)

	// 8. Get after delete should fail
	_, err = svc.Get(ctx, idStr)
	assert.Error(t, err)
}

func TestArticleService_Fanout(t *testing.T) {
	ctx := context.Background()
	client := setupTestDB(t)
	svc := setupArticleService(t, client)

	// Test fanout list initially empty
	rows, err := svc.ListFanout(ctx)
	require.NoError(t, err)
	assert.Empty(t, rows)
}
