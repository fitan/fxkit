package comment

import (
	"context"
	"strings"

	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
)

// Config 是 comments 段配置：列表默认页大小。
type Config struct {
	// PageSize 是列表默认页大小。
	PageSize int `yaml:"page_size"`
}

// SetDefaults 填入默认页大小，供 config.Load 在 YAML 缺省时使用。
func (c *Config) SetDefaults() {
	c.PageSize = 10
}

// Service 是评论用例层；评论本身没有 outbox，只需要 DB 与 repository。
type Service struct {
	db       *gormx.Client
	comments *repository.CommentRepo
	cfg      *Config
}

// NewService 校验依赖，并在 Lifecycle.OnStart 迁移评论表。
func NewService(db *gormx.Client, comments *repository.CommentRepo, cfg *Config, lc *di.Lifecycle) (*Service, error) {
	if db == nil {
		return nil, fxerrors.Internal("comment: nil gormx client")
	}
	if comments == nil {
		return nil, fxerrors.Internal("comment: nil repository")
	}
	if cfg == nil {
		cfg = &Config{}
		cfg.SetDefaults()
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 10
	}
	lc.Append(di.Hook{
		OnStart: func(ctx context.Context) error {
			return entity.MigrateComments(db.Conn(ctx))
		},
	})
	return &Service{db: db, comments: comments, cfg: cfg}, nil
}

// List 在调用方未指定 limit 时使用配置的默认页大小，
// 并把 entity 投影为 API 读模型；repo 本身只返回 entity。
func (s *Service) List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[CommentListRow], error) {
	if params.Limit <= 0 {
		params.Limit = s.cfg.PageSize
	}
	res, err := s.comments.List(ctx, params)
	if err != nil {
		return crudx.ListResult[CommentListRow]{}, err
	}
	return res.Map(toCommentListRow), nil
}

// Create 校验必填项，并从 authz 上下文取作者；正文去首尾空白。
// repo 写入 entity 后，由 service 投影为 API 读模型。
func (s *Service) Create(ctx context.Context, req CreateCommentReq) (CommentListRow, error) {
	if req.ArticleID <= 0 {
		return CommentListRow{}, fxerrors.Validation(map[string]string{"articleId": "required"})
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return CommentListRow{}, fxerrors.Validation(map[string]string{"body": "required"})
	}
	author := ""
	if subj, ok := authz.SubjectFromContext(ctx); ok {
		author = subj.ID
	}
	row := entity.Comment{
		ArticleID: req.ArticleID,
		Body:      body,
		Note:      req.Note,
		Author:    author,
	}
	if err := s.comments.Insert(ctx, &row); err != nil {
		return CommentListRow{}, err
	}
	return toCommentListRow(row), nil
}

// Delete 软删除评论。
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.comments.Delete(ctx, id)
}
