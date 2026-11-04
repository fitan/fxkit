package crudx

import (
	"context"
	"reflect"
	"strings"
	"time"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/gorm"
)

// ListResult 是标准的列表响应封装。
type ListResult[T any] struct {
	Items      []T     `json:"items"`
	Total      *int64  `json:"total,omitempty"`
	Start      int     `json:"start"`
	Limit      int     `json:"limit"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

// Map 用 fn 将 ListResult[T] 转换为 ListResult[R]，并保留分页元数据。
// 借助 Go 1.27+ 的类型泛型方法。
func (lr ListResult[T]) Map[R any](fn func(T) R) ListResult[R] {
	items := make([]R, len(lr.Items))
	for i, item := range lr.Items {
		items[i] = fn(item)
	}
	return ListResult[R]{
		Items:      items,
		Total:      lr.Total,
		Start:      lr.Start,
		Limit:      lr.Limit,
		NextCursor: lr.NextCursor,
	}
}

// ListInput 驱动 GORM 模型 M 到 Row 类型行的通用 [List] 辅助函数。
//
// DB 必须已限定到模型/表，例如：
//
//	client.Conn(ctx).Model(&M{}).Table("things")
//
// ToRow 将每个取出的模型投影为 API 列表行。
type ListInput[M, Row any] struct {
	DB       *gorm.DB                // 已限定范围的基准查询（已设置 Model+Table）
	Spec     ListSpec                // ZStack 风格的过滤/排序注册表
	Params   ListParams              // 请求参数（offset 或游标）
	ToRow    func(M) Row             // 必需的投影
	Select   []string                // 可选的 SELECT 列白名单
	Preload  func(*gorm.DB) *gorm.DB // 可选，在 Find 之前应用的预加载
	Preloads []string                // 可选，需要预加载的关联名，例如 []string{"Profile"}
}

// List 执行 ZStack 风格的白名单列表查询（过滤、排序、offset 或 keyset 游标），
// 并可选返回 count、nextCursor 与 DTO 投影；*gorm.DB 的所有权归调用方。
//
// 内部实现见 listImpl；这里额外记录耗时/行数/错误原因指标。
func List[M, Row any](ctx context.Context, in ListInput[M, Row]) (ListResult[Row], error) {
	start := time.Now()
	res, err := listImpl(ctx, in)
	recordList(ctx, time.Since(start), len(res.Items), err)
	return res, err
}

func listImpl[M, Row any](ctx context.Context, in ListInput[M, Row]) (ListResult[Row], error) {
	if in.ToRow == nil {
		return ListResult[Row]{}, fxerrors.Internal("crudx.List: ToRow is required")
	}
	if in.DB == nil {
		return ListResult[Row]{}, fxerrors.Internal("crudx.List: DB is required")
	}
	meta, err := ResolveModel[M](in.DB)
	if err != nil {
		return ListResult[Row]{}, err
	}

	tx := in.DB.WithContext(ctx)
	selectCols := in.Select
	// 游标需要读取主键来生成 nextCursor；Select 未投影主键时自动补上，
	// 否则 FormatPrimaryKey 会取到零值，第二页直接变空。
	if (in.Params.Cursor != "" || in.Params.UseCursor) && len(selectCols) > 0 {
		if meta.Column == "" {
			return ListResult[Row]{}, InvalidQueryCondition("", ReasonCursorNeedsPK)
		}
		pkCol := qualifiedColumn(meta.Table, meta.Column)
		if !selectHasColumn(selectCols, meta.Column, pkCol) {
			cp := make([]string, 0, len(selectCols)+1)
			cp = append(cp, selectCols...)
			selectCols = append(cp, pkCol)
		}
	}
	if len(selectCols) > 0 {
		cols := make([]any, len(selectCols))
		for i, col := range selectCols {
			cols[i] = col
		}
		tx = tx.Select(cols[0], cols[1:]...)
	}

	params := in.Params
	pkCol := qualifiedColumn(meta.Table, meta.Column)
	// 先应用过滤/排序/游标而不加 Limit/Offset，这样无论 GORM 版本如何 Count 都准确。
	tx, _, err = applyList(tx, in.Spec, &params, listApplyOpts{
		PKColumn:   pkCol,
		PKKind:     meta.Kind,
		SkipPaging: true,
	})
	if err != nil {
		return ListResult[Row]{}, err
	}

	var total *int64
	if params.ReplyWithCount {
		if params.Cursor != "" || params.UseCursor {
			return ListResult[Row]{}, fxerrors.BadRequest("replyWithCount is not supported with cursor pagination")
		}
		var n int64
		// 按 PK 去重，避免过滤条件引入 JOIN 时总数被放大。
		countTx := tx.Session(&gorm.Session{})
		if pkCol != "" {
			countTx = countTx.Distinct(pkCol)
		}
		if err := countTx.Count(&n).Error; err != nil {
			return ListResult[Row]{}, fxerrors.Wrap(err)
		}
		total = &n
	}

	tx = tx.Limit(params.Limit)
	if params.Cursor == "" {
		tx = tx.Offset(params.Start)
	}

	for _, p := range in.Preloads {
		p = strings.TrimSpace(p)
		if p != "" {
			tx = tx.Preload(p)
		}
	}
	if in.Preload != nil {
		tx = in.Preload(tx)
	}

	var rows []M
	if err := tx.Find(&rows).Error; err != nil {
		return ListResult[Row]{}, fxerrors.Wrap(err)
	}

	items := make([]Row, 0, len(rows))
	for _, row := range rows {
		items = append(items, in.ToRow(row))
	}
	out := ListResult[Row]{
		Items: items,
		Total: total,
		Start: params.Start,
		Limit: params.Limit,
	}
	if (params.Cursor != "" || params.UseCursor) && len(rows) == params.Limit && params.Limit > 0 {
		tok, err := nextCursor[M](&rows[len(rows)-1], params, in.Spec, meta)
		if err != nil {
			return ListResult[Row]{}, err
		}
		out.NextCursor = &tok
	}
	return out, nil
}

// GetByID 按 id 字符串加载单行，并通过 toDetail 投影。
// scopes 在 First 之前执行（例如 [Preload]）。
func GetByID[M, Detail any](
	ctx context.Context,
	db *gorm.DB,
	id string,
	toDetail func(M) Detail,
	scopes ...func(*gorm.DB) *gorm.DB,
) (Detail, error) {
	var zero Detail
	if toDetail == nil {
		return zero, fxerrors.Internal("crudx.GetByID: toDetail is required")
	}
	row, err := FirstByID[M](ctx, db, id, scopes...)
	if err != nil {
		return zero, err
	}
	return toDetail(*row), nil
}

// selectHasColumn 报告 cols 是否已投影 name（限定或裸列名）、
// 裸 "*"，或等价的带别名表达式。
func selectHasColumn(cols []string, names ...string) bool {
	for _, c := range cols {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if c == "*" {
			return true
		}
		base := c
		if i := strings.LastIndex(base, "."); i >= 0 {
			base = base[i+1:]
		}
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			if strings.EqualFold(c, n) || strings.EqualFold(base, n) {
				return true
			}
		}
	}
	return false
}

func nextCursor[M any](last *M, params ListParams, spec ListSpec, meta *ModelMeta) (string, error) {
	sortBy, sortDir := params.SortBy, params.SortDirection
	if sortBy == "" {
		sortBy, sortDir = ParseSort(spec.DefaultSort)
	}
	if err := rejectRelationCursor(spec, &params, sortBy); err != nil {
		return "", err
	}
	sv, err := readSortValue(last, sortBy, spec, meta)
	if err != nil {
		return "", err
	}
	pk, err := FormatPrimaryKey[M](last)
	if err != nil {
		return "", err
	}
	return EncodeCursor(sortBy, sortDir, sv, pk)
}

func findFieldValueByColumn(v reflect.Value, base string) (any, bool) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, false
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		fv := v.Field(i)
		dbName := gormColumnName(sf.Name, sf.Tag.Get("gorm"))
		if dbName == base || strings.EqualFold(sf.Name, base) {
			return fv.Interface(), true
		}
		if sf.Anonymous {
			if val, ok := findFieldValueByColumn(fv, base); ok {
				return val, true
			}
		}
	}
	return nil, false
}

func readSortValue[M any](entity *M, sortBy string, spec ListSpec, meta *ModelMeta) (any, error) {
	col := spec.SortFields[sortBy]
	if col == "" {
		if res, err := resolveField(spec, sortBy); err == nil {
			col = res.Column
		}
	}
	if col == "" {
		col = meta.Column
	}
	base := col
	if i := strings.LastIndex(col, "."); i >= 0 {
		base = col[i+1:]
	}
	v := reflect.ValueOf(entity).Elem()
	if val, ok := findFieldValueByColumn(v, base); ok {
		return val, nil
	}
	if meta != nil && (base == meta.Column || strings.EqualFold(meta.FieldName, base)) {
		f := v.FieldByName(meta.FieldName)
		if f.IsValid() {
			return f.Interface(), nil
		}
	}
	return nil, fxerrors.BadRequest("cursor: cannot read sort field %s", sortBy)
}

func gormColumnName(fieldName, gormTag string) string {
	if gormTag != "" {
		for _, p := range strings.Split(gormTag, ";") {
			if strings.HasPrefix(p, "column:") {
				return strings.TrimPrefix(p, "column:")
			}
		}
	}
	return toSnakeColumn(fieldName)
}

func toSnakeColumn(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper {
			if i > 0 {
				prev := runes[i-1]
				prevLower := prev >= 'a' && prev <= 'z' || prev >= '0' && prev <= '9'
				nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
				if prevLower || nextLower {
					b.WriteByte('_')
				}
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
