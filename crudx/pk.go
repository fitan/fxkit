package crudx

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// PKInfo 描述从 GORM model 解析出的主键。
type PKInfo struct {
	FieldName     string // Go 字段名，例如 "ID"
	Column        string // 数据库列名，例如 "id"
	Kind          reflect.Kind
	AutoIncrement bool // GORM schema 标记的主键自增（autoIncrement / 数值主键默认）
}

// autoIncrementNumeric 只有自增数值主键才应该在 create 前清零；
// 客户端生成的 UUID / 业务主键必须保留。
func (p PKInfo) autoIncrementNumeric() bool {
	return p.AutoIncrement && isNumericKind(p.Kind)
}

func isNumericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

// ModelMeta 是 T 的 GORM schema 元数据（主键、资源名、表名），由 [ResolveModel] 解析并缓存。
type ModelMeta struct {
	PKInfo
	Resource string // 结构体名，用于 NotFound 消息，例如 "User"
	Table    string // 数据库表名，例如 "users"
	// ColumnIndex 是「数据库列名 → 结构体字段索引路径」。
	//
	// 由 GORM schema 解析得到，因此尊重自定义 NamingStrategy；
	// 游标分页按列名回读字段值时依赖它，而不是自己推驼峰→下划线。
	ColumnIndex map[string][]int
}

// modelMetaCache 使用 sync.Map，读写已并发安全，无需额外 mutex。
var modelMetaCache sync.Map // reflect.Type -> *ModelMeta

// ResolveModel 解析 T 的 GORM schema（每个类型仅解析一次并缓存）。
// 请在服务启动时调用，以便 [ZeroPrimaryKey] 等无 db 参数的辅助函数可用。
func ResolveModel[T any](db *gorm.DB) (*ModelMeta, error) {
	t := reflect.TypeFor[T]()
	if v, ok := modelMetaCache.Load(t); ok {
		return v.(*ModelMeta), nil
	}
	meta, err := resolveModelUncached[T](db)
	if err != nil {
		return nil, err
	}
	if actual, loaded := modelMetaCache.LoadOrStore(t, meta); loaded {
		return actual.(*ModelMeta), nil
	}
	return meta, nil
}

func resolveModelUncached[T any](db *gorm.DB) (*ModelMeta, error) {
	var zero T
	s, err := schema.Parse(&zero, &sync.Map{}, db.NamingStrategy)
	if err != nil {
		return nil, fmt.Errorf("crudx: parse schema for %T: %w", zero, err)
	}
	if len(s.PrimaryFields) == 0 {
		return nil, fmt.Errorf("crudx: type %T has no primary key (add gorm:\"primaryKey\")", zero)
	}
	if len(s.PrimaryFields) > 1 {
		return nil, fmt.Errorf("crudx: type %T has composite primary key (not supported yet)", zero)
	}
	pf := s.PrimaryFields[0]
	cols := make(map[string][]int, len(s.Fields))
	for _, f := range s.Fields {
		if f == nil || f.DBName == "" || len(f.StructField.Index) == 0 {
			continue
		}
		cols[f.DBName] = f.StructField.Index
	}
	return &ModelMeta{
		PKInfo: PKInfo{
			FieldName:     pf.Name,
			Column:        pf.DBName,
			Kind:          pf.FieldType.Kind(),
			AutoIncrement: pf.AutoIncrement,
		},
		Resource:    s.Name,
		Table:       s.Table,
		ColumnIndex: cols,
	}, nil
}

func modelMetaCached[T any]() (*ModelMeta, bool) {
	v, ok := modelMetaCache.Load(reflect.TypeFor[T]())
	if !ok {
		return nil, false
	}
	return v.(*ModelMeta), true
}

// FormatPrimaryKey 将实体主键格式化为 URL/JSON 安全的 id 字符串。需已调用 [ResolveModel]。
func FormatPrimaryKey[T any](m *T) (string, error) {
	meta, ok := modelMetaCached[T]()
	if !ok {
		return "", fmt.Errorf("crudx: model %T not resolved (call ResolveModel at init)", *new(T))
	}
	f := reflect.ValueOf(m).Elem().FieldByName(meta.FieldName)
	if !f.IsValid() {
		return "", fmt.Errorf("crudx: primary key %s not found on type", meta.FieldName)
	}
	switch meta.Kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(f.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(f.Uint(), 10), nil
	default:
		return fmt.Sprint(f.Interface()), nil
	}
}

// ParseIDString 将路径或 JSON 中的 id 字符串转换为 GORM 期望的标量类型。
func ParseIDString(pk *PKInfo, s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("missing")
	}
	switch pk.Kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.ParseInt(s, 10, 64)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.ParseUint(s, 10, 64)
	default:
		return s, nil
	}
}

// FirstByID 按主键列加载单行。scopes 在 First 之前执行（例如 [Preload]）。
// PK 与 NotFound 资源名从 T 的已缓存 [ModelMeta] 推导；请先调用 [ResolveModel]。
func FirstByID[T any](ctx context.Context, db *gorm.DB, idStr string, scopes ...func(*gorm.DB) *gorm.DB) (*T, error) {
	meta, err := ResolveModel[T](db)
	if err != nil {
		return nil, err
	}
	id, err := ParseIDString(&meta.PKInfo, idStr)
	if err != nil {
		return nil, fxerrors.BadRequest("invalid %s: %v", meta.Column, err)
	}
	tx := db.WithContext(ctx).Where(qualifiedColumn(meta.Table, meta.Column)+" = ?", id)
	for _, sc := range scopes {
		if sc != nil {
			tx = sc(tx)
		}
	}
	var entity T
	if err := tx.First(&entity).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fxerrors.NotFound(meta.Resource, "%s=%v", meta.Column, id)
		}
		return nil, fxerrors.Wrap(err)
	}
	return &entity, nil
}

// ZeroPrimaryKey 在 create 前将自增数值主键置零。需已调用 [ResolveModel]。
// 字符串 / UUID 等客户端生成的主键，以及非自增的数值主键会被保留。
func ZeroPrimaryKey[T any](t *T) {
	meta, ok := modelMetaCached[T]()
	if !ok {
		return
	}
	zeroPrimaryKey(meta, t)
}

func zeroPrimaryKey(meta *ModelMeta, t any) {
	if meta == nil || !meta.autoIncrementNumeric() {
		return
	}
	v := reflect.ValueOf(t)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return
	}
	f := v.FieldByName(meta.FieldName)
	if !f.IsValid() || !f.CanSet() {
		return
	}
	f.Set(reflect.Zero(f.Type()))
}

// CopyPrimaryKey 从 src 复制 PK 到 dst（用于从 DTO body 更新）。需已调用 [ResolveModel]。
func CopyPrimaryKey[T any](src, dst *T) error {
	meta, ok := modelMetaCached[T]()
	if !ok {
		return fmt.Errorf("crudx: model %T not resolved (call ResolveModel at init)", *new(T))
	}
	sv := reflect.ValueOf(src).Elem().FieldByName(meta.FieldName)
	dv := reflect.ValueOf(dst).Elem().FieldByName(meta.FieldName)
	if !sv.IsValid() || !dv.IsValid() || !dv.CanSet() {
		return fmt.Errorf("crudx: primary key %s not addressable on type", meta.FieldName)
	}
	dv.Set(sv)
	return nil
}
