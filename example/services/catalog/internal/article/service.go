package article

import (
	"context"
	"fmt"
	"strings"

	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/outbox"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
)

// Service 是文章用例层：负责事务边界、outbox 入队与 authz 上下文。
// 它不 import huma，因此可以被 worker / CLI 复用。
type Service struct {
	db       *gormx.Client
	articles *repository.ArticleRepo
	store    *outbox.Store
	inbox    *outbox.Inbox
	outbox   *outbox.Config
}

// NewService 校验依赖，并在 Lifecycle.OnStart 迁移文章相关的表。
func NewService(
	db *gormx.Client,
	articles *repository.ArticleRepo,
	store *outbox.Store,
	inbox *outbox.Inbox,
	obcfg *outbox.Config,
	lc *di.Lifecycle,
) (*Service, error) {
	if db == nil {
		return nil, fxerrors.Internal("article: nil gormx client")
	}
	if articles == nil {
		return nil, fxerrors.Internal("article: nil repository")
	}
	if _, err := crudx.ResolveModel[entity.Article](db.Conn(context.Background())); err != nil {
		return nil, err
	}
	s := &Service{
		db:       db,
		articles: articles,
		store:    store,
		inbox:    inbox,
		outbox:   obcfg,
	}
	lc.Append(di.Hook{
		OnStart: func(ctx context.Context) error {
			return entity.MigrateArticles(db.Conn(ctx))
		},
	})
	return s, nil
}

// List 走 repository 的 crudx.ListSpec（字段/排序白名单），
// 并把 entity 投影为 API 读模型；repo 本身只返回 entity。
func (s *Service) List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[ArticleListRow], error) {
	res, err := s.articles.List(ctx, params)
	if err != nil {
		return crudx.ListResult[ArticleListRow]{}, err
	}
	return res.Map(toArticleListRow), nil
}

// Get 按 id 读取文章。
func (s *Service) Get(ctx context.Context, id string) (ArticleDetail, error) {
	m, err := s.articles.Get(ctx, id)
	if err != nil {
		return ArticleDetail{}, err
	}
	return toArticleDetail(m), nil
}

// Create 在同一事务内插入文章并写 outbox 事件；标题重复返回 409。
// 事件 key 用 topic + 文章 ID，保证 relay 重复投递时可被消费端去重。
func (s *Service) Create(ctx context.Context, req CreateArticleReq) (ArticleDetail, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return ArticleDetail{}, fxerrors.Validation(map[string]string{"title": "required"})
	}
	author := ""
	if subj, ok := authz.SubjectFromContext(ctx); ok {
		author = subj.ID
	}
	return s.db.WithTxResult(ctx, func(txCtx context.Context) (ArticleDetail, error) {
		exists, err := s.articles.ExistsTitle(txCtx, title)
		if err != nil {
			return ArticleDetail{}, err
		}
		if exists {
			return ArticleDetail{}, fxerrors.Conflict("article title already exists")
		}
		u := entity.Article{
			Title:   title,
			Body:    req.Body,
			Status:  "draft",
			Author:  author,
			Version: 1,
		}
		crudx.ZeroPrimaryKey(&u)
		if err := s.articles.Insert(txCtx, &u); err != nil {
			return ArticleDetail{}, err
		}
		if s.outbox != nil && s.outbox.Enabled {
			if err := outbox.EnqueueTopicMsg(txCtx, outbox.EnqueueTopicInput[ArticleCreatedEvent]{
				Store: s.store,
				Topic: articleCreatedTopic,
				Msg: ArticleCreatedEvent{
					ID:     u.ID,
					Title:  u.Title,
					Author: u.Author,
				},
				IdempotencyKey: fmt.Sprintf("%s:%d", articleCreatedTopic, u.ID),
			}); err != nil {
				return ArticleDetail{}, fxerrors.Wrap(err)
			}
		}
		return toArticleDetail(u), nil
	})
}

// Update 必须带 version，走 gormx.UpdateWithVersion：
// version 不匹配（别人先改了）会返回 409，而不是静默覆盖。
func (s *Service) Update(ctx context.Context, req UpdateArticleReq) (ArticleDetail, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return ArticleDetail{}, fxerrors.Validation(map[string]string{"title": "required"})
	}
	if req.Version <= 0 {
		return ArticleDetail{}, fxerrors.Validation(map[string]string{"version": "required"})
	}
	return s.db.WithTxResult(ctx, func(txCtx context.Context) (ArticleDetail, error) {
		existing, err := s.articles.Get(txCtx, req.ID)
		if err != nil {
			return ArticleDetail{}, err
		}
		existing.Version = req.Version
		existing.Title = title
		existing.Body = req.Body
		if err := s.articles.UpdateWithVersion(txCtx, &existing, "title", "body"); err != nil {
			return ArticleDetail{}, err
		}
		out, err := s.articles.Get(txCtx, req.ID)
		if err != nil {
			return ArticleDetail{}, err
		}
		return toArticleDetail(out), nil
	})
}

// Delete 软删除文章。
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.articles.Delete(ctx, id)
}

// ListFanout 返回 fan-out 消费者写入的副表数据。
func (s *Service) ListFanout(ctx context.Context) ([]entity.ArticleFanout, error) {
	return s.articles.ListFanout(ctx)
}
