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

// Repo 是可选的泛型 GORM 辅助类型。写操作优先使用 [List] 加显式的 service 方法；
// 新代码无需内嵌 *Repo[T]。
type Repo[T any] struct {
	client        *gormx.Client
	spec          ListSpec
	updateColumns []string
	meta          *ModelMeta
}

// RepoConfig 配置 [NewRepo]。
type RepoConfig struct {
	Client        *gormx.Client
	Spec          ListSpec
	UpdateColumns []string // [Repo.Update] 必需，除非设置了 UpdateInput.Columns
}

// UpdateInput 是 [Repo.Update] 的写入载荷。
type UpdateInput[T any] struct {
	Entity  *T
	Columns []string // 可选的 Select 白名单；为空时使用 RepoConfig.UpdateColumns
}

// ListOpt 定制 [Repo.List]（预加载 / select）。
type ListOpt struct {
	Preload []string
	Select  []string
}

// WhereInput 驱动 [Repo.First] / [Repo.Exists]。
type WhereInput struct {
	Query  string // 例如 "email = ?"
	Args   []any
	Detail string // 可选的 NotFound 详情，例如 "email=a@example.com"
}

// NewRepo 构造可选的类型化仓储。若使用 [Repo]，在装配时调用一次即可；
// 大多数 service 应改用 [List] / [FirstByID]。
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

// Client 返回底层 gormx 客户端（用于 outbox / 共享事务辅助函数）。
func (r *Repo[T]) Client() *gormx.Client { return r.client }

// Spec 返回列表过滤/排序注册表。
func (r *Repo[T]) Spec() ListSpec { return r.spec }

// Meta 返回缓存的 GORM 模型元数据。
func (r *Repo[T]) Meta() *ModelMeta { return r.meta }

// DB 返回已绑定模型 T 及其表的请求级查询。
func (r *Repo[T]) DB(ctx context.Context) *gorm.DB {
	var zero T
	return r.client.Conn(ctx).Model(&zero).Table(r.meta.Table)
}

// Migrate 对 T 执行 AutoMigrate。
func (r *Repo[T]) Migrate(ctx context.Context) error {
	var zero T
	return r.client.Conn(ctx).AutoMigrate(&zero)
}

// Transaction 在数据库事务内执行 fn（借助 gormx 支持嵌套）。
func (r *Repo[T]) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.client.Transaction(ctx, fn)
}

// Create 插入 entity。仅自增数值主键会被清零；
// 客户端生成的主键（UUID / 业务 ID）会被保留。
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

// GetByID 按主键字符串加载单行。
func (r *Repo[T]) GetByID(ctx context.Context, id string) (*T, error) {
	return FirstByID[T](ctx, r.DB(ctx), id)
}

// ListTo 对 T 执行 ZStack 风格列表查询，并将每个实体映射为行 DTO 类型 R。
// 借助 Go 1.27+ 的类型泛型方法。
func (r *Repo[T]) ListTo[R any](ctx context.Context, params ListParams, toRow func(T) R, opts ...ListOpt) (ListResult[R], error) {
	var opt ListOpt
	if len(opts) > 0 {
		opt = opts[0]
	}
	in := ListInput[T, R]{
		DB:     r.DB(ctx),
		Spec:   r.spec,
		Params: params,
		Select: opt.Select,
		ToRow:  toRow,
	}
	if len(opt.Preload) > 0 {
		in.Preload = Preload(opt.Preload...)
	}
	return List(ctx, in)
}

// GetByIDTo 按主键加载单行，并将其映射为目标类型 R。
// 借助 Go 1.27+ 的类型泛型方法。
func (r *Repo[T]) GetByIDTo[R any](ctx context.Context, id string, toRow func(T) R) (R, error) {
	return GetByID[T, R](ctx, r.DB(ctx), id, toRow)
}

// List 对 T 执行 ZStack 风格列表查询，并返回模型行。
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

// Update 按主键定位实体并写入指定列。
// 先检查存在性，避免值未变化时误报 NotFound
// （MySQL 在数据完全相同时可能返回 RowsAffected=0）。
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

	pkCol := qualifiedColumn(r.meta.Table, r.meta.Column)
	var n int64
	if err := r.DB(ctx).Where(pkCol+" = ?", pk).Limit(1).Count(&n).Error; err != nil {
		return MapDBError(err)
	}
	if n == 0 {
		return fxerrors.NotFound(r.meta.Resource, "%s=%v", r.meta.Column, pk)
	}

	res := r.DB(ctx).Select(cols).Where(pkCol+" = ?", pk).Updates(in.Entity)
	if res.Error != nil {
		return MapDBError(res.Error)
	}
	return nil
}

// Delete 删除 id 字符串标识的行（单条语句）。
func (r *Repo[T]) Delete(ctx context.Context, id string) error {
	idVal, err := ParseIDString(&r.meta.PKInfo, id)
	if err != nil {
		return fxerrors.BadRequest("invalid %s: %v", r.meta.Column, err)
	}
	var zero T
	pkCol := qualifiedColumn(r.meta.Table, r.meta.Column)
	res := r.DB(ctx).Where(pkCol+" = ?", idVal).Delete(&zero)
	if res.Error != nil {
		return MapDBError(res.Error)
	}
	if res.RowsAffected == 0 {
		return fxerrors.NotFound(r.meta.Resource, "%s=%v", r.meta.Column, idVal)
	}
	return nil
}

// First 加载匹配 in.Query 的第一行。
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

// FirstTo 加载匹配 in.Query 的第一行，并将其映射为目标类型 R。
// 借助 Go 1.27+ 的类型泛型方法。
func (r *Repo[T]) FirstTo[R any](ctx context.Context, in WhereInput, toRow func(T) R) (R, error) {
	var zero R
	entity, err := r.First(ctx, in)
	if err != nil {
		return zero, err
	}
	return toRow(*entity), nil
}

// FirstWhere 加载匹配 query 的第一行（例如 "email = ?", email）。
// 需要更清晰的 NotFound 消息时优先用带 Detail 的 [First]。
func (r *Repo[T]) FirstWhere(ctx context.Context, query string, args ...any) (*T, error) {
	return r.First(ctx, WhereInput{Query: query, Args: args})
}

// Exists 报告是否有行匹配 in.Query。
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

// ExistsWhere 报告是否有行匹配 query。
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
