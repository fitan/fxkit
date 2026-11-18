package repository

import (
	"context"

	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/gormx"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

// CommentRepo 封装评论表的读写；返回 entity，API 读模型由 service 投影。
type CommentRepo struct {
	db   *gormx.Client
	spec crudx.ListSpec
}

// NewCommentRepo 绑定 DB 与列表白名单。
func NewCommentRepo(db *gormx.Client) *CommentRepo {
	return &CommentRepo{db: db, spec: commentListSpec()}
}

// List 返回实体列表（支持按 article.title 过滤，JOIN 只用于 WHERE）；
// 显式 identity ToRow 直接透传，API 读模型由 service 投影。
func (r *CommentRepo) List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[entity.Comment], error) {
	return crudx.List(ctx, crudx.ListInput[entity.Comment, entity.Comment]{
		DB:     r.db.Conn(ctx).Model(&entity.Comment{}).Table("comments"),
		Spec:   r.spec,
		Params: params,
		ToRow:  func(c entity.Comment) entity.Comment { return c },
	})
}

// Get 按主键读取评论。
func (r *CommentRepo) Get(ctx context.Context, id string) (entity.Comment, error) {
	row, err := crudx.FirstByID[entity.Comment](ctx, r.db.Conn(ctx), id)
	if err != nil {
		return entity.Comment{}, err
	}
	return *row, nil
}

// Insert 写入评论；GORM 会把自增 ID / 时间回填到 c。
func (r *CommentRepo) Insert(ctx context.Context, c *entity.Comment) error {
	return crudx.MapDBError(r.db.Conn(ctx).Create(c).Error)
}

// Delete 软删除评论。
func (r *CommentRepo) Delete(ctx context.Context, id string) error {
	row, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	return crudx.MapDBError(r.db.Conn(ctx).Delete(&row).Error)
}

// commentListSpec 的 Relations 与 Fields/SortFields 同级并列，
// JOIN 条件直接写在 On 里，不需要额外的嵌套 builder。
func commentListSpec() crudx.ListSpec {
	return crudx.ListSpec{
		Fields: map[string]crudx.FieldSpec{
			"id":         {Column: "comments.id", Kind: crudx.FieldNumber},
			"article_id": {Column: "comments.article_id", Kind: crudx.FieldNumber, Indexed: true},
			// body/note 是 TEXT 列：MySQL 无法建索引，用 Likeable 显式承认
			// 全表扫描成本（既有行为，保留但明确标注）。
			"body":       {Column: "comments.body", Kind: crudx.FieldString, Likeable: true},
			"note":       {Column: "comments.note", Kind: crudx.FieldString, Likeable: true},
			"author":     {Column: "comments.author", Kind: crudx.FieldString, Indexed: true},
			"created_at": {Column: "comments.created_at", Kind: crudx.FieldTime},
		},
		Relations: map[string]*crudx.RelationSpec{
			"article": {
				Table: "articles",
				Alias: "article",
				On:    "article.id = comments.article_id",
				Fields: map[string]crudx.FieldSpec{
					"title": {Column: "article.title", Kind: crudx.FieldString, Indexed: true, Likeable: true},
				},
			},
		},
		SortFields: map[string]string{
			"id":         "comments.id",
			"created_at": "comments.created_at",
		},
		DefaultSort: "id",
		Limits:      crudx.Limits{LimitDefault: 10, LimitMax: 100},
	}
}
