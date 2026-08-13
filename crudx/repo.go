package crudx

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"gorm.io/gorm"
)

// Repo is a one-layer generic GORM repository for model T.
// Feature packages embed *Repo[T] and only add domain-specific queries.
type Repo[T any] struct {
	client        *gormx.Client
	spec          ListSpec
	updateColumns []string
	meta          *ModelMeta
}

// RepoConfig configures [NewRepo].
type RepoConfig struct {
	Client        *gormx.Client
	Spec          ListSpec
	UpdateColumns []string // required for [Repo.Update] unless UpdateInput.Columns is set
}

// UpdateInput is the write payload for [Repo.Update].
type UpdateInput[T any] struct {
	Entity  *T
	Columns []string // optional Select whitelist; empty uses RepoConfig.UpdateColumns
}

// ListOpt customizes [Repo.List] (preload / select).
type ListOpt struct {
	Preload []string
	Select  []string
}

// WhereInput drives [Repo.First] / [Repo.Exists].
type WhereInput struct {
	Query  string // e.g. "email = ?"
	Args   []any
	Detail string // optional NotFound detail, e.g. "email=a@example.com"
}

// NewRepo constructs a typed repository. Call once at wiring time (e.g. Fx Provide).
func NewRepo[T any](cfg RepoConfig) (*Repo[T], error) {
	if cfg.Client == nil || cfg.Client.Pool() == nil {
		return nil, fxerrors.Internal("crudx.Repo: nil gorm client")
	}
	meta, err := ResolveModel[T](cfg.Client.Pool())
	if err != nil {
		return nil, err
	}
	return &Repo[T]{
		client:        cfg.Client,
		spec:          cfg.Spec,
		updateColumns: append([]string(nil), cfg.UpdateColumns...),
		meta:          meta,
	}, nil
}

// Client returns the underlying gormx client (for outbox / shared tx helpers).
func (r *Repo[T]) Client() *gormx.Client { return r.client }

// Spec returns the list filter/sort registry.
func (r *Repo[T]) Spec() ListSpec { return r.spec }

// Meta returns cached GORM model metadata.
func (r *Repo[T]) Meta() *ModelMeta { return r.meta }

// DB returns a request-scoped query already bound to model T / its table.
func (r *Repo[T]) DB(ctx context.Context) *gorm.DB {
	var zero T
	return r.client.Conn(ctx).Model(&zero).Table(r.meta.Table)
}

// Migrate runs AutoMigrate for T.
func (r *Repo[T]) Migrate(ctx context.Context) error {
	var zero T
	return r.client.Conn(ctx).AutoMigrate(&zero)
}

// Transaction runs fn inside a DB transaction (nested-safe via gormx).
func (r *Repo[T]) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.client.Transaction(ctx, fn)
}

// Create inserts entity (zeroes PK first).
func (r *Repo[T]) Create(ctx context.Context, entity *T) error {
	if entity == nil {
		return fxerrors.Internal("crudx.Repo.Create: nil entity")
	}
	ZeroPrimaryKey(entity)
	if err := r.DB(ctx).Create(entity).Error; err != nil {
		return MapDBError(err)
	}
	return nil
}

// GetByID loads one row by primary-key string.
func (r *Repo[T]) GetByID(ctx context.Context, id string) (*T, error) {
	return FirstByID[T](ctx, r.DB(ctx), id)
}

// List runs ZStack-style list against T and returns model rows.
func (r *Repo[T]) List(ctx context.Context, params ListParams, opts ...ListOpt) (ListResult[T], error) {
	var opt ListOpt
	if len(opts) > 0 {
		opt = opts[0]
	}
	in := ListInput[T, T]{
		DB:     r.DB(ctx),
		Spec:   r.spec,
		Params: params,
		Select: opt.Select,
		ToRow:  func(row T) T { return row },
	}
	if len(opt.Preload) > 0 {
		in.Preload = Preload(opt.Preload...)
	}
	return List(ctx, in)
}

// Update writes columns on the entity identified by its primary key.
// Existence is checked first so unchanged values never surface as NotFound
// (MySQL may report RowsAffected=0 when data is identical).
func (r *Repo[T]) Update(ctx context.Context, in UpdateInput[T]) error {
	if in.Entity == nil {
		return fxerrors.Internal("crudx.Repo.Update: nil entity")
	}
	pk, err := primaryKeyValue(r.meta, in.Entity)
	if err != nil {
		return err
	}
	if isZeroValue(pk) {
		return fxerrors.BadRequest("%s is required", r.meta.Column)
	}
	cols := in.Columns
	if len(cols) == 0 {
		cols = r.updateColumns
	}
	if len(cols) == 0 {
		return fxerrors.Internal("crudx.Repo.Update: no UpdateColumns configured")
	}

	var n int64
	if err := r.DB(ctx).Where(r.meta.Column+" = ?", pk).Limit(1).Count(&n).Error; err != nil {
		return MapDBError(err)
	}
	if n == 0 {
		return fxerrors.NotFound(r.meta.Resource, "%s=%v", r.meta.Column, pk)
	}

	res := r.DB(ctx).Select(cols).Where(r.meta.Column+" = ?", pk).Updates(in.Entity)
	if res.Error != nil {
		return MapDBError(res.Error)
	}
	return nil
}

// Delete removes the row identified by id string (single statement).
func (r *Repo[T]) Delete(ctx context.Context, id string) error {
	idVal, err := ParseIDString(&r.meta.PKInfo, id)
	if err != nil {
		return fxerrors.BadRequest("invalid %s: %v", r.meta.Column, err)
	}
	var zero T
	res := r.DB(ctx).Where(r.meta.Column+" = ?", idVal).Delete(&zero)
	if res.Error != nil {
		return MapDBError(res.Error)
	}
	if res.RowsAffected == 0 {
		return fxerrors.NotFound(r.meta.Resource, "%s=%v", r.meta.Column, idVal)
	}
	return nil
}

// First loads the first row matching in.Query.
func (r *Repo[T]) First(ctx context.Context, in WhereInput) (*T, error) {
	if in.Query == "" {
		return nil, fxerrors.Internal("crudx.Repo.First: empty query")
	}
	var entity T
	err := r.DB(ctx).Where(in.Query, in.Args...).First(&entity).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if in.Detail != "" {
				return nil, fxerrors.NotFound(r.meta.Resource, "%s", in.Detail)
			}
			return nil, fxerrors.NotFound(r.meta.Resource, "")
		}
		return nil, MapDBError(err)
	}
	return &entity, nil
}

// FirstWhere loads the first row matching query (e.g. "email = ?", email).
// Prefer [First] with Detail for clearer NotFound messages.
func (r *Repo[T]) FirstWhere(ctx context.Context, query string, args ...any) (*T, error) {
	return r.First(ctx, WhereInput{Query: query, Args: args})
}

// Exists reports whether any row matches in.Query.
func (r *Repo[T]) Exists(ctx context.Context, in WhereInput) (bool, error) {
	if in.Query == "" {
		return false, fxerrors.Internal("crudx.Repo.Exists: empty query")
	}
	var n int64
	if err := r.DB(ctx).Where(in.Query, in.Args...).Limit(1).Count(&n).Error; err != nil {
		return false, MapDBError(err)
	}
	return n > 0, nil
}

// ExistsWhere reports whether any row matches query.
func (r *Repo[T]) ExistsWhere(ctx context.Context, query string, args ...any) (bool, error) {
	return r.Exists(ctx, WhereInput{Query: query, Args: args})
}

func primaryKeyValue(meta *ModelMeta, entity any) (any, error) {
	v := reflect.ValueOf(entity)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return nil, fxerrors.Internal("crudx: entity must be a non-nil pointer")
	}
	f := v.Elem().FieldByName(meta.FieldName)
	if !f.IsValid() {
		return nil, fmt.Errorf("crudx: primary key %s not found on type", meta.FieldName)
	}
	return f.Interface(), nil
}

func isZeroValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.IsZero()
}
