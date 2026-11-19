package crudx

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/fitan/fxkit/fxerrors"
	"gorm.io/gorm"
)

// listApplyOpts 配置一次 applyList 调用的游标与分页行为。
type listApplyOpts struct {
	// PKColumn 是根表主键列（可带表前缀），用于游标谓词与一对多 JOIN 去重；为空时游标不可用。
	PKColumn string
	// PKKind 是主键的 Go 类型，用于把游标里的主键字符串还原为对应类型。
	PKKind reflect.Kind
	// SkipPaging 为 true 时不追加 LIMIT/OFFSET（供先 Count 后分页的 [List] 使用）。
	SkipPaging bool
}

// ApplyList 应用 q= 过滤、排序、JOIN 与 LIMIT/OFFSET。
// 当 tx 绑定到具有单一主键的 GORM 模型时 keyset 游标可用（schema 主键从 tx 读取）。
// 需要 total、DTO 投影或 nextCursor 时优先用 [List]：ApplyList 已应用 Limit，
// 因此在返回的 session 上做 Count 会被截断。
func ApplyList(tx *gorm.DB, spec ListSpec, params *ListParams) (*gorm.DB, error) {
	if tx == nil {
		return nil, fxerrors.Internal("crudx.ApplyList: nil db")
	}
	if params == nil {
		return nil, fxerrors.Internal("crudx.ApplyList: nil params")
	}
	out, _, err := applyList(tx, spec, params, pkOptsFromDB(tx))
	return out, err
}

// pkOptsFromDB 从 tx 的 schema 推导主键列与主键类型；非单一主键时返回零值。
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

// qualifiedColumn 给未带前缀的列名加上 table 前缀。
func qualifiedColumn(table, column string) string {
	if column == "" {
		return ""
	}
	if table == "" || strings.Contains(column, ".") {
		return column
	}
	return table + "." + column
}

// applyList 是 [ApplyList] 与 [List] 共用的实现：应用过滤、JOIN、排序、游标与分页，
// 并返回本次 JOIN 数。opts.SkipPaging 为 true 时不加 LIMIT/OFFSET。
func applyList(tx *gorm.DB, spec ListSpec, params *ListParams, opts listApplyOpts) (*gorm.DB, int, error) {
	if tx == nil {
		return nil, 0, fxerrors.Internal("crudx.ApplyList: nil db")
	}
	if params == nil {
		return nil, 0, fxerrors.Internal("crudx.ApplyList: nil params")
	}
	spec.Normalize(params)
	if err := spec.validateIdentifiers(); err != nil {
		return nil, 0, err
	}

	groups, err := ParseQGroups(spec, params.Q)
	if err != nil {
		return nil, 0, err
	}

	joined := map[string]struct{}{}
	lim := spec.Limits.normalized()
	joinCount := 0
	// needDistinct：只有「可能放大行数」的 JOIN（未知基数或一对多）才需要 DISTINCT。
	// 多对一/一对一关联跳过 DISTINCT，既省一次排序去重，也避免 PG 的
	// "ORDER BY expressions must appear in select list" 限制。
	needDistinct := false

	ensureJoins := func(rawQ string, joins []joinPlan) error {
		for _, j := range joins {
			if _, ok := joined[j.Alias]; ok {
				continue
			}
			joinCount++
			if joinCount > lim.MaxJoins {
				return InvalidQueryCondition(rawQ, ReasonTooManyJoins)
			}
			if j.Cardinality != RelationToOne {
				needDistinct = true
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
	hasCursor := params.Cursor != ""
	var cursorSortVal any
	var cursorPK string
	if hasCursor {
		csb, csd, sortVal, pk, err := DecodeCursor(params.Cursor)
		if err != nil {
			return nil, joinCount, err
		}
		// 游标内的排序是权威的：翻页时客户端可以只带 cursor（direction 为空会被
		// Normalize 归一为 asc）。只有调用方显式传入且与游标不一致才算冲突。
		// 游标内的排序是权威的：传输层（例如 Huma 的 `default:"asc"`）会在
		// 客户端未传时填入默认值，无法区分「显式传入」与「框架默认」。
		// 因此方向冲突一律以游标为准，不报错（否则 desc 翻页第二页必 400）。
		if csb != "" && params.sortByExplicit && csb != params.SortBy {
			return nil, joinCount, InvalidQueryCondition("", ReasonInvalidSortField)
		}
		if csb != "" {
			sortBy = csb
		}
		if csd != "" {
			sortDir = csd
		}
		cursorSortVal, cursorPK = sortVal, pk
	}
	if strings.TrimSpace(sortBy) == "" && spec.DefaultSort != "" {
		sortBy, sortDir = ParseSort(spec.DefaultSort)
	}
	col, dir, ok := validatedSortPath(spec, sortBy, sortDir)
	if !ok {
		// 游标分页必须有确定排序：否则首页没有 ORDER BY（顺序随机）却返回
		// nextCursor，次页又因为 cursor 无法解析出排序列而 400。
		if params.sortByExplicit || hasCursor || params.UseCursor {
			return nil, joinCount, InvalidQueryCondition("", ReasonInvalidSortField)
		}
	}
	if ok {
		if res, err := resolveFieldForSort(spec, sortBy); err == nil {
			if err := ensureJoins("", res.Joins); err != nil {
				return nil, joinCount, err
			}
		}
		if err := rejectRelationCursor(spec, params, sortBy); err != nil {
			return nil, joinCount, err
		}
		// NULL 统一排在最后：PG/MySQL/SQLite 都把 false 排在 true 之前。
		//
		// 但 PostgreSQL 不允许 `SELECT DISTINCT` 下 ORDER BY 引用未出现在 SELECT
		// 列表里的表达式（SQLSTATE 42P10）。一对多 JOIN 需要 DISTINCT 时只能
		// 跳过该表达式，NULL 排序交给数据库默认（仅影响可空排序列）。
		if !needDistinct {
			tx = tx.Order(fmt.Sprintf("(%s IS NULL)", col))
		}
		tx = tx.Order(fmt.Sprintf("%s %s", col, dir))
		if opts.PKColumn != "" && !isSameColumn(opts.PKColumn, col) {
			tx = tx.Order(fmt.Sprintf("%s %s", opts.PKColumn, dir))
		}
		// 记录实际生效的排序，供 List 生成 nextCursor 时使用（游标翻页时 params
		// 里的 direction 可能已被空值归一成 asc）。
		params.SortBy, params.SortDirection = sortBy, dir
	}

	if hasCursor {
		if opts.PKColumn == "" {
			return nil, joinCount, InvalidQueryCondition("", ReasonCursorNeedsPK)
		}
		if !ok {
			return nil, joinCount, InvalidQueryCondition("", ReasonInvalidSortField)
		}
		pk, err := coerceCursorPK(cursorPK, opts.PKKind)
		if err != nil {
			return nil, joinCount, err
		}
		sql, args, err := cursorWhereSQL(col, opts.PKColumn, dir, cursorSortVal, pk)
		if err != nil {
			return nil, joinCount, err
		}
		tx = tx.Where(sql, args...)
	}

	if joinCount > 0 && needDistinct {
		// 一对多 JOIN 会放大行数，必须 DISTINCT；此时按关联列排序在各方言上
		// 语义不一致（PG 直接拒绝：ORDER BY 表达式必须出现在 SELECT 列表）。
		// 与其让 PG 报 500、SQLite “看起来能用”，不如在各方言上统一为 400。
		if ok && isForeignColumn(col, opts.PKColumn) {
			return nil, joinCount, InvalidQueryCondition("", ReasonDistinctRelationSort)
		}
		// 对根模型去重以合并 1:N JOIN 产生的重复行。
		//
		// 不用 `SELECT DISTINCT *`：PostgreSQL 对 json/jsonb/array 列没有等值操作符，
		// DISTINCT * 会直接报错。按主键 GROUP BY 在各方言上都能工作
		// （PG/MySQL 支持主键函数依赖，SQLite 允许非聚合列）。
		if opts.PKColumn != "" {
			tx = tx.Group(opts.PKColumn)
		} else {
			tx = tx.Distinct()
		}
	}

	if !opts.SkipPaging {
		tx = tx.Limit(params.Limit)
		if params.Cursor == "" {
			tx = tx.Offset(params.Start)
		}
	}
	return tx, joinCount, nil
}

// isForeignColumn 判断列是否不属于根表（即来自 JOIN 的关联表）。
func isForeignColumn(col, pkColumn string) bool {
	i := strings.LastIndex(col, ".")
	if i < 0 {
		return false
	}
	j := strings.LastIndex(pkColumn, ".")
	if j < 0 {
		return false
	}
	return col[:i] != pkColumn[:j]
}

// isSameColumn 报告两个列名是否指向同一列（允许一边带表前缀）。
func isSameColumn(a, b string) bool {
	if a == b {
		return true
	}
	if idx := strings.LastIndex(a, "."); idx >= 0 && a[idx+1:] == b {
		return true
	}
	if idx := strings.LastIndex(b, "."); idx >= 0 && b[idx+1:] == a {
		return true
	}
	return false
}

// rejectRelationCursor 在游标分页下拒绝按关联列排序（返回 400）；非游标模式直接放行。
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

// resolveFieldForSort 解析排序字段：不含点号时优先查 SortFields，否则走 [resolveField]。
func resolveFieldForSort(spec ListSpec, sortBy string) (resolvedField, error) {
	if col, ok := spec.SortFields[sortBy]; ok && !strings.Contains(sortBy, ".") {
		return resolvedField{Column: col}, nil
	}
	return resolveField(spec, sortBy)
}

// applyCondition 按 c.Op 为单个条件生成 WHERE；nil 值转为 IS NULL / IS NOT NULL。
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
		// 用 '!' 作转义符：MySQL 默认 sql_mode 下反斜杠会转义引号。
		return tx.Where(col+" LIKE ? ESCAPE '!'", "%"+escapeLikePattern(s)+"%"), nil
	case OpNotLike:
		s, ok := c.Values[0].(string)
		if !ok {
			return nil, InvalidQueryCondition("", ReasonInvalidValueType)
		}
		return tx.Where(col+" NOT LIKE ? ESCAPE '!'", "%"+escapeLikePattern(s)+"%"), nil
	case OpGT, OpGTE, OpLT, OpLTE:
		if len(c.Values) == 0 || c.Values[0] == nil {
			return nil, InvalidQueryCondition(c.RawQ, ReasonInvalidValueType)
		}
		cmp := map[Operator]string{OpGT: " > ?", OpGTE: " >= ?", OpLT: " < ?", OpLTE: " <= ?"}
		return tx.Where(col+cmp[c.Op], c.Values[0]), nil
	case OpIn:
		vals, hasNull := splitNullValues(c.Values)
		switch {
		case len(vals) == 0 && !hasNull:
			return tx.Where("1 = 0"), nil
		case len(vals) == 0:
			return tx.Where(col + " IS NULL"), nil
		case hasNull:
			// SQL 的 IN (NULL) 永远不成立，必须显式合并 IS NULL。
			return tx.Where("("+col+" IN ? OR "+col+" IS NULL)", vals), nil
		default:
			return tx.Where(col+" IN ?", vals), nil
		}
	case OpNotIn:
		vals, hasNull := splitNullValues(c.Values)
		switch {
		case len(vals) == 0 && !hasNull:
			return tx, nil
		case len(vals) == 0:
			return tx.Where(col + " IS NOT NULL"), nil
		case hasNull:
			// NOT IN (NULL) 对任何行都是 UNKNOWN，必须显式排除 NULL。
			return tx.Where("("+col+" NOT IN ? AND "+col+" IS NOT NULL)", vals), nil
		default:
			return tx.Where(col+" NOT IN ?", vals), nil
		}
	default:
		return tx, nil
	}
}

// validatedSortPath 把排序键解析为列名与归一化方向；无法排序时 ok 为 false。
func validatedSortPath(spec ListSpec, sortBy, sortDir string) (col, dir string, ok bool) {
	sortBy = strings.TrimSpace(sortBy)
	if sortBy == "" {
		if spec.DefaultSort != "" {
			sortBy, sortDir = ParseSort(spec.DefaultSort)
		} else {
			return "", "", false
		}
	}
	// SortFields 优先（允许排序键与过滤键不同名）。
	col, ok = spec.SortFields[sortBy]
	if !ok {
		// StrictSort：只允许显式白名单（SortFields）或带索引的列。
		// 关系路径（含 "."）仍然放行：它们由 Relations 显式声明。
		if spec.StrictSort && !strings.Contains(sortBy, ".") {
			fs, ferr := resolveFieldSpec(spec, sortBy)
			if ferr != nil || !fs.Indexed {
				return "", "", false
			}
			col, ok = fs.Column, true
		} else if res, err := resolveField(spec, sortBy); err == nil {
			col = res.Column
			ok = true
		}
	}
	// 空列名无法生成 ORDER BY（FieldSpec 存在但没填 Column 的字段不应可排序）。
	if !ok || col == "" {
		return "", "", false
	}
	dir = normalizeSortDir(sortDir)
	if dir == "" {
		return "", "", false
	}
	return col, dir, true
}

// coerceCursorPK 把游标里的主键字符串转换为模型主键的类型；解析失败返回 400。
func coerceCursorPK(pk string, kind reflect.Kind) (any, error) {
	pk = strings.TrimSpace(pk)
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(pk, 10, 64)
		if err != nil {
			return nil, InvalidQueryCondition("", ReasonInvalidCursorPK)
		}
		return i, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(pk, 10, 64)
		if err != nil {
			return nil, InvalidQueryCondition("", ReasonInvalidCursorPK)
		}
		return u, nil
	}
	return pk, nil
}

// escapeLikePattern 转义 LIKE 的转义符 '!' 以及 % 和 _，使用户输入
// 在 LIKE ... ESCAPE '!' 中按字面量匹配。
func escapeLikePattern(s string) string {
	replacer := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return replacer.Replace(s)
}

// splitNullValues 将 nil 元素与真正的 IN/NOT IN 值分开。
func splitNullValues(values []any) ([]any, bool) {
	out := make([]any, 0, len(values))
	hasNull := false
	for _, v := range values {
		if v == nil {
			hasNull = true
			continue
		}
		out = append(out, v)
	}
	return out, hasNull
}
