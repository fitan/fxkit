package article

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/outbox"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"go.uber.org/fx"
)

const articleCreatedTopic = "article-created"

// Article is the GORM model. JSON tags are "-" because the API uses DTOs.
type Article struct {
	ID        int64     `gorm:"primaryKey" json:"-"`
	Title     string    `gorm:"size:255;not null;index" json:"-"`
	Body      string    `gorm:"type:text" json:"-"`
	Status    string    `gorm:"size:32;not null;index" json:"-"`
	Author    string    `gorm:"size:128;index" json:"-"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

type ArticleListRow struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Author string `json:"author,omitempty"`
}

type ArticleDetail struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	Status    string    `json:"status"`
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type CreateArticleReq struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

type UpdateArticleReq struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// ArticleCreatedEvent is the outbox / Hatchet payload for article-created.
type ArticleCreatedEvent struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Author string `json:"author"`
}

// ArticleFanout is a second consumer of the same Hatchet event (fan-out by task name).
type ArticleFanout struct {
	ID        int64  `gorm:"primaryKey"`
	ArticleID int64  `gorm:"index;not null"`
	Title     string `gorm:"size:255"`
	CreatedAt time.Time
}

func (ArticleFanout) TableName() string { return "catalog_article_fanout" }

type Service struct {
	client   *gormx.Client
	store    *outbox.Store
	inbox    *outbox.Inbox
	outbox   *outbox.Config
	listSpec crudx.ListSpec
}

func NewService(client *gormx.Client, store *outbox.Store, inbox *outbox.Inbox, obcfg *outbox.Config, lc fx.Lifecycle) (*Service, error) {
	if client == nil {
		return nil, fxerrors.Internal("article: nil gormx client")
	}
	if _, err := crudx.ResolveModel[Article](client.Conn(context.Background())); err != nil {
		return nil, err
	}
	s := &Service{
		client: client,
		store:  store,
		inbox:  inbox,
		outbox: obcfg,
		listSpec: crudx.ListSpec{
			Fields: map[string]crudx.FieldSpec{
				"id":     {Column: "articles.id", Kind: crudx.FieldNumber},
				"title":  {Column: "articles.title", Kind: crudx.FieldString, Indexed: true},
				"status": {Column: "articles.status", Kind: crudx.FieldString, Indexed: true},
				"author": {Column: "articles.author", Kind: crudx.FieldString, Indexed: true},
			},
			SortFields: map[string]string{
				"id":         "articles.id",
				"title":      "articles.title",
				"status":     "articles.status",
				"created_at": "articles.created_at",
			},
			DefaultSort: "id",
			Limits:      crudx.Limits{LimitDefault: 20, LimitMax: 100},
		},
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var z Article
			if err := client.Conn(ctx).AutoMigrate(&z, &ArticleFanout{}); err != nil {
				return err
			}
			return nil
		},
	})
	return s, nil
}

func (s *Service) List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[ArticleListRow], error) {
	return crudx.List(ctx, crudx.ListInput[Article, ArticleListRow]{
		DB:     s.client.Conn(ctx).Model(&Article{}).Table("articles"),
		Spec:   s.listSpec,
		Params: params,
		ToRow:  toArticleListRow,
	})
}

func (s *Service) Get(ctx context.Context, id string) (ArticleDetail, error) {
	return crudx.GetByID[Article, ArticleDetail](ctx, s.client.Conn(ctx), id, toArticleDetail)
}

func (s *Service) Create(ctx context.Context, req CreateArticleReq) (ArticleDetail, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return ArticleDetail{}, fxerrors.Validation(map[string]string{"title": "required"})
	}
	author := ""
	if subj, ok := authz.SubjectFromContext(ctx); ok {
		author = subj.ID
	}
	var out ArticleDetail
	err := s.client.Transaction(ctx, func(txCtx context.Context) error {
		var n int64
		if err := s.client.Conn(txCtx).Model(&Article{}).Where("title = ?", title).Count(&n).Error; err != nil {
			return crudx.MapDBError(err)
		}
		if n > 0 {
			return fxerrors.Conflict("article title already exists")
		}
		u := Article{
			Title:  title,
			Body:   req.Body,
			Status: "draft",
			Author: author,
		}
		crudx.ZeroPrimaryKey(&u)
		if err := s.client.Conn(txCtx).Create(&u).Error; err != nil {
			return crudx.MapDBError(err)
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
				return fxerrors.Wrap(err)
			}
		}
		out = toArticleDetail(u)
		return nil
	})
	if err != nil {
		return ArticleDetail{}, err
	}
	return out, nil
}

func (s *Service) Update(ctx context.Context, req UpdateArticleReq) (ArticleDetail, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return ArticleDetail{}, fxerrors.Validation(map[string]string{"title": "required"})
	}
	existing, err := crudx.FirstByID[Article](ctx, s.client.Conn(ctx), req.ID)
	if err != nil {
		return ArticleDetail{}, err
	}
	next := *existing
	next.Title = title
	next.Body = req.Body
	if err := s.client.Conn(ctx).Select("title", "body").Updates(&next).Error; err != nil {
		return ArticleDetail{}, crudx.MapDBError(err)
	}
	out, err := crudx.FirstByID[Article](ctx, s.client.Conn(ctx), req.ID)
	if err != nil {
		return ArticleDetail{}, err
	}
	return toArticleDetail(*out), nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	row, err := crudx.FirstByID[Article](ctx, s.client.Conn(ctx), id)
	if err != nil {
		return err
	}
	if err := s.client.Conn(ctx).Delete(row).Error; err != nil {
		return crudx.MapDBError(err)
	}
	return nil
}

func (s *Service) HandleCreated(ctx context.Context, ev ArticleCreatedEvent) error {
	if s.inbox == nil {
		return s.markIndexed(ctx, ev.ID)
	}
	return s.inbox.Once(ctx, outbox.OnceInput{
		Key:   fmt.Sprintf("%s:%d", articleCreatedTopic, ev.ID),
		Topic: articleCreatedTopic,
		Fn: func(ctx context.Context) error {
			return s.markIndexed(ctx, ev.ID)
		},
	})
}

func (s *Service) HandleCreatedFanout(ctx context.Context, ev ArticleCreatedEvent) error {
	fn := func(ctx context.Context) error {
		row := ArticleFanout{ArticleID: ev.ID, Title: ev.Title}
		if err := s.client.Conn(ctx).Create(&row).Error; err != nil {
			return crudx.MapDBError(err)
		}
		slog.Info("article-created fan-out", "article_id", ev.ID, "title", ev.Title)
		return nil
	}
	if s.inbox == nil {
		return fn(ctx)
	}
	return s.inbox.Once(ctx, outbox.OnceInput{
		Key:   fmt.Sprintf("%s-fanout:%d", articleCreatedTopic, ev.ID),
		Topic: articleCreatedTopic,
		Fn:    fn,
	})
}

func (s *Service) ListFanout(ctx context.Context) ([]ArticleFanout, error) {
	var rows []ArticleFanout
	err := s.client.Conn(ctx).Order("id desc").Limit(20).Find(&rows).Error
	if err != nil {
		return nil, crudx.MapDBError(err)
	}
	return rows, nil
}

func (s *Service) markIndexed(ctx context.Context, id int64) error {
	res := s.client.Conn(ctx).Model(&Article{}).
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

func RegisterWorker(svc *Service) hatchetx.Registrar {
	return func(client *hatchetx.Client) ([]hatchetx.WorkflowBase, error) {
		if client == nil || !client.Enabled() {
			return nil, nil
		}
		task := client.SDK.NewStandaloneTask(
			"catalog-on-article-created",
			func(ctx hatchet.Context, ev ArticleCreatedEvent) (map[string]any, error) {
				err := svc.HandleCreated(ctx, ev)
				return map[string]any{"ok": err == nil, "id": ev.ID}, err
			},
			hatchet.WithWorkflowEvents(articleCreatedTopic),
		)
		// Same event key, different task name = MQ fan-out (second consumer group).
		fanout := client.SDK.NewStandaloneTask(
			"catalog-on-article-created-fanout",
			func(ctx hatchet.Context, ev ArticleCreatedEvent) (map[string]any, error) {
				err := svc.HandleCreatedFanout(ctx, ev)
				return map[string]any{"ok": err == nil, "id": ev.ID}, err
			},
			hatchet.WithWorkflowEvents(articleCreatedTopic),
		)
		return []hatchetx.WorkflowBase{task, fanout}, nil
	}
}

var articleResource = fxhuma.RegisterResourceInput[UpdateArticleReq]{
	Path:        "/articles",
	Tags:        []string{"Articles"},
	ListSummary: "List articles (q= / cursor)",
	BindUpdate: func(id string, body UpdateArticleReq) UpdateArticleReq {
		body.ID = id
		return body
	},
}

func NewHumaRegistrar(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.RegisterResource(api, svc, articleResource)
		fxhuma.Register(api, fanoutOp, func(ctx context.Context, _ *struct{}) (*fanoutOut, error) {
			rows, err := svc.ListFanout(ctx)
			if err != nil {
				return nil, err
			}
			return &fanoutOut{Body: fanoutBody{Items: rows}}, nil
		})
	})
}

type fanoutBody struct {
	Items []ArticleFanout `json:"items"`
}

type fanoutOut struct {
	Body fanoutBody
}

var fanoutOp = huma.Operation{
	OperationID: "listArticleFanout",
	Method:      http.MethodGet,
	Path:        "/article-fanout",
	Summary:     "Second Hatchet consumer of article-created (fan-out)",
	Tags:        []string{"Articles"},
}

func articleHTTPRoutes() []authz.HTTPRoute {
	ops := append(fxhuma.ResourceOperations(articleResource), fanoutOp)
	return authz.HTTPRoutesFromOperations(ops...)
}

func toArticleListRow(m Article) ArticleListRow {
	return ArticleListRow{ID: m.ID, Title: m.Title, Status: m.Status, Author: m.Author}
}

func toArticleDetail(m Article) ArticleDetail {
	return ArticleDetail{
		ID:        m.ID,
		Title:     m.Title,
		Body:      m.Body,
		Status:    m.Status,
		Author:    m.Author,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

var Module = fx.Options(
	fxkit.Service("article", NewService),
	fxhuma.ProvideRegistrar(NewHumaRegistrar),
	hatchetx.ProvideRegistrar(RegisterWorker),
	authz.ProvideHTTPRoutes(articleHTTPRoutes),
)
