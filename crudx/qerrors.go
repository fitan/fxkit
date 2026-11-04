package crudx

import "github.com/fitan/fxkit/fxerrors"

// 查询错误原因码（ZStack 风格的 q= 解析）。
const (
	ReasonNoOperator             = "NO_OPERATOR"
	ReasonFieldNotExist          = "FIELD_NOT_EXIST"
	ReasonRelationDepthOverLimit = "RELATION_DEPTH_OVER_LIMIT"
	ReasonInvalidValueType       = "INVALID_VALUE_TYPE"
	ReasonEmptyLikeValue         = "EMPTY_LIKE_VALUE"
	ReasonTooManyConditions      = "TOO_MANY_CONDITIONS"
	ReasonIllegalORSyntax        = "ILLEGAL_OR_SYNTAX"
	ReasonTooManyInValues        = "TOO_MANY_IN_VALUES"
	ReasonTooManyJoins           = "TOO_MANY_JOINS"
	ReasonTooManyLikeConditions  = "TOO_MANY_LIKE_CONDITIONS"
	ReasonFieldNotIndexed        = "FIELD_NOT_INDEXED"
	ReasonInvalidSortField       = "INVALID_SORT_FIELD"
	// ReasonDistinctRelationSort: 一对多 JOIN 需要 DISTINCT 时，不能按关联列排序
	// （PG 要求 ORDER BY 表达式出现在 SELECT 列表；MySQL 语义也不确定）。
	ReasonDistinctRelationSort = "DISTINCT_RELATION_SORT_UNSUPPORTED"
	ReasonCursorNeedsPK        = "CURSOR_NEEDS_PRIMARY_KEY"
	ReasonCursorRelationSort   = "CURSOR_RELATION_SORT_UNSUPPORTED"
)

// InvalidQueryCondition 为 q= 解析/校验失败返回 HTTP 400。
func InvalidQueryCondition(rawQ, reason string) *fxerrors.Error {
	return fxerrors.BadRequest("查询条件解析失败").WithDetails(map[string]any{
		"code":   "INVALID_QUERY_CONDITION",
		"raw_q":  rawQ,
		"reason": reason,
	})
}
