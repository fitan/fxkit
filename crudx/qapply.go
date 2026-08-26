package crudx

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

type listApplyOpts struct {
	PKColumn   string
	PKKind     reflect.Kind
	SkipPaging bool
}

// ApplyList applies q= filters, sort, JOINs, and LIMIT/OFFSET.
// Keyset cursor works when tx is bound to a GORM model with a single primary key
// (schema PK is read from tx). Prefer [List] when you need total, DTO projection,
// or nextCursor: ApplyList already applies Limit, so Count on the returned session
// is truncated.
func ApplyList(tx *gorm.DB, spec ListSpec, params *ListParams) (*gorm.DB, error) {
	out, _, err := applyList(tx, spec, params, pkOptsFromDB(tx))
	return out, err
}

func pkOptsFromDB(tx *gorm.DB) listApplyOpts {
	if tx == nil || tx.Statement == nil {
		return listApplyOpts{}
	}
	if tx.Statement.Schema == nil && tx.Statement.Model != nil {
		_ = tx.Statement.Parse(tx.Statement.Model)
	}
	if tx.Statement.Schema == nil {
		return listApplyOpts{}
	}
	pfs := tx.Statement.Schema.PrimaryFields
	if len(pfs) != 1 {
		return listApplyOpts{}
	}
	return listApplyOpts{
		PKColumn: qualifiedColumn(tx.Statement.Schema.Table, pfs[0].DBName),
		PKKind:   pfs[0].FieldType.Kind(),
	}
}

func qualifiedColumn(table, column string) string {
	if column == "" {
		return ""
	}
	if table == "" || strings.Contains(column, ".") {
		return column
	}
	return table + "." + column
}

func applyList(tx *gorm.DB, spec ListSpec, params *ListParams, opts listApplyOpts) (*gorm.DB, int, error) {
	spec = deriveSortFields(spec)
	spec.Normalize(params)
	groups, err := ParseQGroups(spec, params.Q)
	if err != nil {
		return nil, 0, err
	}

	joined := map[string]struct{}{}
	lim := spec.Limits.normalized()
	joinCount := 0

	ensureJoins := func(rawQ string, joins []joinPlan) error {
		for _, j := range joins {
			if _, ok := joined[j.Alias]; ok {
				continue
			}
			joinCount++
			if joinCount > lim.MaxJoins {
				return InvalidQueryCondition(rawQ, ReasonTooManyJoins)
			}
			tx = tx.Joins(j.SQL)
			joined[j.Alias] = struct{}{}
		}
		return nil
	}

	for _, g := range groups {
		if len(g.Conds) == 0 {
			continue
		}
		if !g.Or {
			for _, c := range g.Conds {
				c = NormalizeEqualToIn(c)
				res, err := resolveField(spec, c.Field)
				if err != nil {
					return nil, joinCount, InvalidQueryCondition(c.RawQ, ReasonFieldNotExist)
				}
				if err := ensureJoins(c.RawQ, res.Joins); err != nil {
					return nil, joinCount, err
				}
				tx, err = applyCondition(tx, res.Column, c)
				if err != nil {
					return nil, joinCount, err
				}
			}
			continue
		}
		var orDB *gorm.DB
		for i, c := range g.Conds {
			c = NormalizeEqualToIn(c)
			res, err := resolveField(spec, c.Field)
			if err != nil {
				return nil, joinCount, InvalidQueryCondition(c.RawQ, ReasonFieldNotExist)
			}
			if err := ensureJoins(c.RawQ, res.Joins); err != nil {
				return nil, joinCount, err
			}
			part := tx.Session(&gorm.Session{NewDB: true})
			part, err = applyCondition(part, res.Column, c)
			if err != nil {
				return nil, joinCount, err
			}
			if i == 0 {
				orDB = part
			} else {
				orDB = orDB.Or(part)
			}
		}
		if orDB != nil {
			tx = tx.Where(orDB)
		}
	}

	sortBy, sortDir := params.SortBy, params.SortDirection
	col, dir, ok := validatedSortPath(spec, sortBy, sortDir)
	if ok {
		if res, err := resolveFieldForSort(spec, params.SortBy); err == nil {
			if err := ensureJoins("", res.Joins); err != nil {
				return nil, joinCount, err
			}
		}
		if err := rejectRelationCursor(spec, params, sortBy); err != nil {
			return nil, joinCount, err
		}
		tx = tx.Order(fmt.Sprintf("%s %s", col, dir))
		if opts.PKColumn != "" && opts.PKColumn != col {
			tx = tx.Order(fmt.Sprintf("%s %s", opts.PKColumn, dir))
		}
	}

	if params.Cursor != "" {
		if opts.PKColumn == "" {
			return nil, joinCount, InvalidQueryCondition("", ReasonCursorNeedsPK)
		}
		csb, csd, sortVal, pk, err := DecodeCursor(params.Cursor)
		if err != nil {
			return nil, joinCount, err
		}
		if csb != "" && params.SortBy != "" && csb != params.SortBy {
			return nil, joinCount, InvalidQueryCondition("", ReasonInvalidSortField)
		}
		if csd != "" && params.SortDirection != "" && csd != params.SortDirection {
			return nil, joinCount, InvalidQueryCondition("", ReasonInvalidSortField)
		}
		if !ok {
			col, dir, ok = validatedSortPath(spec, csb, csd)
			if !ok {
				return nil, joinCount, InvalidQueryCondition("", ReasonInvalidSortField)
			}
		}
		if err := rejectRelationCursor(spec, params, firstNonEmpty(params.SortBy, csb)); err != nil {
			return nil, joinCount, err
		}
		sql, err := cursorWhereSQL(col, opts.PKColumn, dir)
		if err != nil {
			return nil, joinCount, err
		}
		tx = tx.Where(sql, sortVal, sortVal, coerceCursorPK(pk, opts.PKKind))
	}

	if joinCount > 0 {
		// SELECT DISTINCT of the root model collapses 1:N JOIN duplicates.
		tx = tx.Distinct()
	}

	if !opts.SkipPaging {
		tx = tx.Limit(params.Limit)
		if params.Cursor == "" {
			tx = tx.Offset(params.Start)
		}
	}
	return tx, joinCount, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func rejectRelationCursor(spec ListSpec, params *ListParams, sortBy string) error {
	if params == nil || (params.Cursor == "" && !params.UseCursor) {
		return nil
	}
	sortBy = strings.TrimSpace(sortBy)
	if sortBy == "" {
		return nil
	}
	if strings.Contains(sortBy, ".") {
		return InvalidQueryCondition("", ReasonCursorRelationSort)
	}
	if res, err := resolveFieldForSort(spec, sortBy); err == nil && len(res.Joins) > 0 {
		return InvalidQueryCondition("", ReasonCursorRelationSort)
	}
	return nil
}

func resolveFieldForSort(spec ListSpec, sortBy string) (resolvedField, error) {
	if col, ok := spec.SortFields[sortBy]; ok && !strings.Contains(sortBy, ".") {
		return resolvedField{Column: col}, nil
	}
	return resolveField(spec, sortBy)
}

func applyCondition(tx *gorm.DB, col string, c Condition) (*gorm.DB, error) {
	switch c.Op {
	case OpEqual:
		if len(c.Values) == 1 && c.Values[0] == nil {
			return tx.Where(col + " IS NULL"), nil
		}
		return tx.Where(col+" = ?", c.Values[0]), nil
	case OpNotEqual:
		if len(c.Values) == 1 && c.Values[0] == nil {
			return tx.Where(col + " IS NOT NULL"), nil
		}
		return tx.Where(col+" <> ?", c.Values[0]), nil
	case OpIsNull:
		return tx.Where(col + " IS NULL"), nil
	case OpNotNull:
		return tx.Where(col + " IS NOT NULL"), nil
	case OpLike:
		s, ok := c.Values[0].(string)
		if !ok {
			return nil, InvalidQueryCondition("", ReasonInvalidValueType)
		}
		return tx.Where(col+` LIKE ? ESCAPE '\'`, "%"+escapeLikePattern(s)+"%"), nil
	case OpNotLike:
		s, ok := c.Values[0].(string)
		if !ok {
			return nil, InvalidQueryCondition("", ReasonInvalidValueType)
		}
		return tx.Where(col+` NOT LIKE ? ESCAPE '\'`, "%"+escapeLikePattern(s)+"%"), nil
	case OpGT, OpGTE, OpLT, OpLTE:
		if len(c.Values) == 0 || c.Values[0] == nil {
			return nil, InvalidQueryCondition(c.RawQ, ReasonInvalidValueType)
		}
		cmp := map[Operator]string{OpGT: " > ?", OpGTE: " >= ?", OpLT: " < ?", OpLTE: " <= ?"}
		return tx.Where(col+cmp[c.Op], c.Values[0]), nil
	case OpIn:
		return tx.Where(col+" IN ?", c.Values), nil
	case OpNotIn:
		return tx.Where(col+" NOT IN ?", c.Values), nil
	default:
		return tx, nil
	}
}

func validatedSortPath(spec ListSpec, sortBy, sortDir string) (col, dir string, ok bool) {
	if len(spec.SortFields) == 0 {
		return "", "", false
	}
	sortBy = strings.TrimSpace(sortBy)
	if sortBy == "" {
		if spec.DefaultSort != "" {
			sortBy, sortDir = ParseSort(spec.DefaultSort)
		} else {
			return "", "", false
		}
	}
	col, ok = spec.SortFields[sortBy]
	if !ok {
		if res, err := resolveField(spec, sortBy); err == nil {
			col = res.Column
			ok = true
		}
	}
	if !ok {
		return "", "", false
	}
	dir = normalizeSortDir(sortDir)
	if dir == "" {
		return "", "", false
	}
	return col, dir, true
}

func coerceCursorPK(pk string, kind reflect.Kind) any {
	pk = strings.TrimSpace(pk)
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if i, err := strconv.ParseInt(pk, 10, 64); err == nil {
			return i
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if u, err := strconv.ParseUint(pk, 10, 64); err == nil {
			return u
		}
	}
	return pk
}

// escapeLikePattern escapes \, %, and _ so user input is matched literally inside LIKE.
func escapeLikePattern(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}
