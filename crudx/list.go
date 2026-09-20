package crudx

import (
	"context"
	"reflect"
	"strings"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/gorm"
)

// ListResult is the standard list response envelope.
type ListResult[T any] struct {
	Items      []T     `json:"items"`
	Total      *int64  `json:"total,omitempty"`
	Start      int     `json:"start"`
	Limit      int     `json:"limit"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

// ListInput drives the generic [List] helper for GORM model M into rows of type Row.
//
// DB must already be scoped to the model/table, e.g.:
//
//	client.Conn(ctx).Model(&M{}).Table("things")
//
// ToRow projects each fetched model into the API list row.
type ListInput[M, Row any] struct {
	DB       *gorm.DB                // scoped base query (Model+Table already set)
	Spec     ListSpec                // ZStack-style filter/sort registry
	Params   ListParams              // request params (offset or cursor)
	ToRow    func(M) Row             // required projection
	Select   []string                // optional SQL column whitelist for SELECT
	Preload  func(*gorm.DB) *gorm.DB // optional preload applied before Find
	Preloads []string                // optional association names to preload e.g. []string{"Profile"}
}

// List runs a ZStack-style list query (filters, sort, offset or keyset cursor),
// optional count, nextCursor, and DTO projection. Callers own the *gorm.DB.
func List[M, Row any](ctx context.Context, in ListInput[M, Row]) (ListResult[Row], error) {
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
	if len(in.Select) > 0 {
		cols := make([]any, len(in.Select))
		for i, col := range in.Select {
			cols[i] = col
		}
		tx = tx.Select(cols[0], cols[1:]...)
	}

	params := in.Params
	pkCol := qualifiedColumn(meta.Table, meta.Column)
	// Filter/sort/cursor without Limit/Offset so Count is accurate regardless of GORM version.
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
		// Distinct on PK avoids inflated totals when filters introduce JOINs.
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

// GetByID loads one row by id string and projects it via toDetail.
// scopes run before First (e.g. [Preload]).
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
