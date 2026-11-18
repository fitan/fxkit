package repository

import (
	"context"

	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

// ArticleRepo 封装文章表的读写与列表查询。
// 所有方法只返回/接收 entity，不依赖 feature 的 API 读模型。
type ArticleRepo struct {
	db   *gormx.Client
	spec crudx.ListSpec
}

// NewArticleRepo 绑定 DB 与文章列表白名单。
func NewArticleRepo(db *gormx.Client) *ArticleRepo {
	return &ArticleRepo{db: db, spec: articleListSpec()}
}

// List 返回实体列表；显式 identity ToRow 直接透传，API 读模型由 service 投影。
func (r *ArticleRepo) List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[entity.Article], error) {
	return crudx.List(ctx, crudx.ListInput[entity.Article, entity.Article]{
		DB:     r.db.Conn(ctx).Model(&entity.Article{}).Table("articles"),
		Spec:   r.spec,
		Params: params,
		ToRow:  func(a entity.Article) entity.Article { return a },
	})
}

// Get 按主键读取文章；不存在时由 crudx 映射为 404。
func (r *ArticleRepo) Get(ctx context.Context, id string) (entity.Article, error) {
	row, err := crudx.FirstByID[entity.Article](ctx, r.db.Conn(ctx), id)
	if err != nil {
		return entity.Article{}, err
	}
	return *row, nil
}

// ExistsTitle 用于创建前的重名校验，由唯一索引/事务共同兜底。
func (r *ArticleRepo) ExistsTitle(ctx context.Context, title string) (bool, error) {
	var n int64
	if err := r.db.Conn(ctx).Model(&entity.Article{}).Where("title = ?", title).Count(&n).Error; err != nil {
		return false, crudx.MapDBError(err)
	}
	return n > 0, nil
}

// Insert 写入新文章。
func (r *ArticleRepo) Insert(ctx context.Context, a *entity.Article) error {
	return crudx.MapDBError(r.db.Conn(ctx).Create(a).Error)
}

// Update 按 fields 白名单更新指定列。
func (r *ArticleRepo) Update(ctx context.Context, a *entity.Article, fields ...string) error {
	q := r.db.Conn(ctx).Model(a)
	if len(fields) > 0 {
		q = q.Select(fields)
	}
	return crudx.MapDBError(q.Updates(a).Error)
}

// UpdateWithVersion 执行乐观锁更新；冲突返回 409。
func (r *ArticleRepo) UpdateWithVersion(ctx context.Context, a *entity.Article, fields ...string) error {
	return gormx.UpdateWithVersion(ctx, r.db, a, fields...)
}

// Delete 软删除文章。
func (r *ArticleRepo) Delete(ctx context.Context, id string) error {
	row, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	return crudx.MapDBError(r.db.Conn(ctx).Delete(&row).Error)
}

// MarkIndexed 是 event 消费者写回的状态变更；行不存在时返回 404。
func (r *ArticleRepo) MarkIndexed(ctx context.Context, id int64) error {
	res := r.db.Conn(ctx).Model(&entity.Article{}).
		Where("id = ?", id).
		Update("status", "indexed")
	if res.Error != nil {
		return crudx.MapDBError(res.Error)
	}
	if res.RowsAffected == 0 {
		return fxerrors.NotFound("Article", "id=%d", id)
	}
	return nil
}

// InsertFanout 写入 fan-out 副表（第二个消费组专用）。
func (r *ArticleRepo) InsertFanout(ctx context.Context, row *entity.ArticleFanout) error {
	return crudx.MapDBError(r.db.Conn(ctx).Create(row).Error)
}

// ListFanout 返回最近 20 条 fan-out 记录。
func (r *ArticleRepo) ListFanout(ctx context.Context) ([]entity.ArticleFanout, error) {
	var rows []entity.ArticleFanout
	if err := r.db.Conn(ctx).Order("id desc").Limit(20).Find(&rows).Error; err != nil {
		return nil, crudx.MapDBError(err)
	}
	return rows, nil
}

// articleListSpec 是过滤/排序白名单。使用 map 字面量而非链式 builder：
// 每行自包含，换行位置不影响语义，也不会出现「modifier 绑错字段」的隐式状态问题。
func articleListSpec() crudx.ListSpec {
	return crudx.ListSpec{
		Fields: map[string]crudx.FieldSpec{
			"id":     {Column: "articles.id", Kind: crudx.FieldNumber},
			"title":  {Column: "articles.title", Kind: crudx.FieldString, Indexed: true, Likeable: true},
			"status": {Column: "articles.status", Kind: crudx.FieldString, Indexed: true},
			"author": {Column: "articles.author", Kind: crudx.FieldString, Indexed: true, Likeable: true},
			// body 是 TEXT 列，故意不开放过滤/LIKE，避免全表扫描
		},
		SortFields: map[string]string{
			"id":         "articles.id",
			"title":      "articles.title",
			"status":     "articles.status",
			"created_at": "articles.created_at",
		},
		DefaultSort: "id",
		Limits:      crudx.Limits{LimitDefault: 20, LimitMax: 100},
	}
}
