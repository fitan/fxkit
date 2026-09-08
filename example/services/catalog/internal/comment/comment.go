package comment

import (
	"context"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	fxhuma "github.com/fitan/fxkit/huma"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

// Comment is a second resource used for explicit Huma Register (not RegisterResource)
// plus crudx q= / cursor / JOIN / isnull.
type Comment struct {
	ID        int64          `gorm:"primaryKey" json:"-"`
	ArticleID int64          `gorm:"index;not null" json:"-"`
	Body      string         `gorm:"type:text" json:"-"`
	Note      *string        `gorm:"size:255" json:"-"`
	Author    string         `gorm:"size:128;index" json:"-"`
	CreatedAt time.Time      `json:"-"`
	UpdatedAt time.Time      `json:"-"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type CommentRow struct {
	ID        int64     `json:"id"`
	ArticleID int64     `json:"articleId"`
	Body      string    `json:"body"`
	Note      *string   `json:"note,omitempty"`
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type CreateCommentReq struct {
	ArticleID int64   `json:"articleId"`
	Body      string  `json:"body"`
	Note      *string `json:"note,omitempty"`
}

type Config struct {
	PageSize int `yaml:"page_size"`
}

func (c *Config) SetDefaults() {
	c.PageSize = 10
}

type Service struct {
	client   *gormx.Client
	cfg      *Config
	listSpec crudx.ListSpec
}

func NewService(client *gormx.Client, cfg *Config, lc fx.Lifecycle) (*Service, error) {
	if client == nil {
		return nil, fxerrors.Internal("comment: nil gormx client")
	}
	if cfg == nil {
		cfg = &Config{}
		cfg.SetDefaults()
	}
	page := cfg.PageSize
	if page <= 0 {
		page = 10
	}
	s := &Service{
		client: client,
		cfg:    cfg,
		listSpec: crudx.ListSpec{
			Fields: map[string]crudx.FieldSpec{
				"id":         {Column: "comments.id", Kind: crudx.FieldNumber},
				"article_id": {Column: "comments.article_id", Kind: crudx.FieldNumber, Indexed: true},
				"body":       {Column: "comments.body", Kind: crudx.FieldString, Indexed: true},
				"note":       {Column: "comments.note", Kind: crudx.FieldString, Indexed: true},
				"author":     {Column: "comments.author", Kind: crudx.FieldString, Indexed: true},
				"created_at": {Column: "comments.created_at", Kind: crudx.FieldTime},
			},
			Relations: map[string]*crudx.RelationSpec{
				"article": {
					Table: "articles",
					Alias: "article",
					On:    "article.id = comments.article_id",
					Fields: map[string]crudx.FieldSpec{
						"title": {Column: "article.title", Kind: crudx.FieldString, Indexed: true},
					},
				},
			},
			SortFields: map[string]string{
				"id":         "comments.id",
				"created_at": "comments.created_at",
			},
			DefaultSort: "id",
			Limits:      crudx.Limits{LimitDefault: page, LimitMax: 100},
		},
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var z Comment
			return client.Conn(ctx).AutoMigrate(&z)
		},
	})
	return s, nil
}

func (s *Service) List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[CommentRow], error) {
	return crudx.List(ctx, crudx.ListInput[Comment, CommentRow]{
		DB:     s.client.Conn(ctx).Model(&Comment{}).Table("comments"),
		Spec:   s.listSpec,
		Params: params,
		ToRow:  toRow,
	})
}

func (s *Service) Create(ctx context.Context, req CreateCommentReq) (CommentRow, error) {
	if req.ArticleID <= 0 {
		return CommentRow{}, fxerrors.Validation(map[string]string{"articleId": "required"})
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return CommentRow{}, fxerrors.Validation(map[string]string{"body": "required"})
	}
	author := ""
	if subj, ok := authz.SubjectFromContext(ctx); ok {
		author = subj.ID
	}
	row := Comment{
		ArticleID: req.ArticleID,
		Body:      body,
		Note:      req.Note,
		Author:    author,
	}
	if err := s.client.Conn(ctx).Create(&row).Error; err != nil {
		return CommentRow{}, crudx.MapDBError(err)
	}
	return toRow(row), nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	row, err := crudx.FirstByID[Comment](ctx, s.client.Conn(ctx), id)
	if err != nil {
		return err
	}
	if err := s.client.Conn(ctx).Delete(row).Error; err != nil {
		return crudx.MapDBError(err)
	}
	return nil
}

type listInput struct {
	fxhuma.ListQueryInput
}

type listOutput struct {
	Body crudx.ListResult[CommentRow]
}

type createInput struct {
	Body CreateCommentReq
}

type createOutput struct {
	Body CommentRow
}

type idPath struct {
	ID string `path:"id"`
}

func Register(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.Register(api, listOp, func(ctx context.Context, in *listInput) (*listOutput, error) {
			params, err := fxhuma.ListParamsFromInput(&in.ListQueryInput)
			if err != nil {
				return nil, err
			}
			out, err := svc.List(ctx, params)
			if err != nil {
				return nil, err
			}
			return &listOutput{Body: out}, nil
		})
		fxhuma.Register(api, createOp, func(ctx context.Context, in *createInput) (*createOutput, error) {
			row, err := svc.Create(ctx, in.Body)
			if err != nil {
				return nil, err
			}
			return &createOutput{Body: row}, nil
		})
		fxhuma.Register(api, deleteOp, func(ctx context.Context, in *idPath) (*struct{}, error) {
			if err := svc.Delete(ctx, in.ID); err != nil {
				return nil, err
			}
			return &struct{}{}, nil
		})
	})
}

var (
	listOp = huma.Operation{
		OperationID: "listComments",
		Method:      "GET",
		Path:        "/comments",
		Summary:     "List comments (q= / cursor / JOIN article.title)",
		Tags:        []string{"Comments"},
	}
	createOp = huma.Operation{
		OperationID: "createComment",
		Method:      "POST",
		Path:        "/comments",
		Summary:     "Create a comment",
		Tags:        []string{"Comments"},
	}
	deleteOp = huma.Operation{
		OperationID: "deleteComment",
		Method:      "DELETE",
		Path:        "/comments/{id}",
		Summary:     "Soft-delete a comment",
		Tags:        []string{"Comments"},
	}
)

func commentHTTPRoutes() []authz.HTTPRoute {
	return authz.HTTPRoutesFromOperations(listOp, createOp, deleteOp)
}

func toRow(m Comment) CommentRow {
	return CommentRow{
		ID:        m.ID,
		ArticleID: m.ArticleID,
		Body:      m.Body,
		Note:      m.Note,
		Author:    m.Author,
		CreatedAt: m.CreatedAt,
	}
}

var Module = fx.Options(
	fxkit.Service("comment", NewService,
		fxkit.ProvideConfig[Config]("comments"),
	),
	fxhuma.ProvideRegistrar(Register),
	authz.ProvideHTTPRoutes(commentHTTPRoutes),
)
