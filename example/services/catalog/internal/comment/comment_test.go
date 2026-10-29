package comment_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/fitan/fxkit-example/services/catalog/internal/article"
	"github.com/fitan/fxkit-example/services/catalog/internal/comment"
	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *gormx.Client {
	dsn := filepath.Join(t.TempDir(), "comment_test.db")
	client, err := gormx.New(&gormx.Config{
		Driver:   "sqlite",
		DSN:      dsn,
		LogLevel: "silent",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	return client
}

func setupCommentService(t *testing.T, client *gormx.Client) *comment.Service {
	lc := di.NewLifecycle()
	cfg := &comment.Config{PageSize: 10}

	svc, err := comment.NewService(client, cfg, lc)
	require.NoError(t, err)
	require.NotNil(t, svc)

	// 也自动迁移 Article 表以支持 JOIN 查询
	require.NoError(t, client.Conn(context.Background()).AutoMigrate(&article.Article{}))
	require.NoError(t, lc.Start(context.Background()))
	return svc
}

func TestCommentService_CRUDAndFilters(t *testing.T) {
	ctx := context.Background()
	client := setupTestDB(t)
	svc := setupCommentService(t, client)

	// 先创建关联文章
	art := article.Article{Title: "Golang Testing", Status: "published", Author: "alice"}
	require.NoError(t, client.Conn(ctx).Create(&art).Error)

	noteVal := "pinned note"

	// 1. Create Comment 1 (带 note)
	c1, err := svc.Create(ctx, comment.CreateCommentReq{
		ArticleID: art.ID,
		Body:      "First comment body",
		Note:      &noteVal,
	})
	require.NoError(t, err)
	assert.True(t, c1.ID > 0)
	assert.Equal(t, art.ID, c1.ArticleID)
	assert.Equal(t, "First comment body", c1.Body)
	assert.Equal(t, &noteVal, c1.Note)

	// 2. Create Comment 2 (Note 为空)
	c2, err := svc.Create(ctx, comment.CreateCommentReq{
		ArticleID: art.ID,
		Body:      "Second comment without note",
	})
	require.NoError(t, err)
	assert.True(t, c2.ID > 0)
	assert.Nil(t, c2.Note)

	// 3. Validation - Empty Body
	_, err = svc.Create(ctx, comment.CreateCommentReq{
		ArticleID: art.ID,
		Body:      "   ",
	})
	assert.Error(t, err)

	// 4. Validation - Zero ArticleID
	_, err = svc.Create(ctx, comment.CreateCommentReq{
		ArticleID: 0,
		Body:      "valid body",
	})
	assert.Error(t, err)

	// 5. List with isNull filter (note.isnull)
	resNull, err := svc.List(ctx, crudx.ListParams{
		Q:     []string{"note.isnull"},
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, len(resNull.Items))
	assert.Equal(t, c2.ID, resNull.Items[0].ID)

	// 6. List with or() filter
	resOr, err := svc.List(ctx, crudx.ListParams{
		Q:     []string{"or(body~First,body~Second)"},
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, len(resOr.Items))

	// 7. Delete (软删除)
	err = svc.Delete(ctx, fmt.Sprintf("%d", c1.ID))
	require.NoError(t, err)

	// 删除后再查，应该只剩 c2
	resAfterDel, err := svc.List(ctx, crudx.ListParams{
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, len(resAfterDel.Items))
	assert.Equal(t, c2.ID, resAfterDel.Items[0].ID)
}
