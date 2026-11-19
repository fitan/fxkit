package crudx

import "github.com/fitan/fxkit/fxerrors"

// 查询错误原因码（ZStack 风格的 q= 解析）。
const (
	// ReasonNoOperator 表示表达式中没有可识别的操作符。
	ReasonNoOperator = "NO_OPERATOR"
	// ReasonFieldNotExist 表示字段不在 ListSpec 白名单内。
	ReasonFieldNotExist = "FIELD_NOT_EXIST"
	// ReasonRelationDepthOverLimit 表示字段路径超过三段。
	ReasonRelationDepthOverLimit = "RELATION_DEPTH_OVER_LIMIT"
	// ReasonInvalidValueType 表示值无法转换为字段声明的 [FieldKind]。
	ReasonInvalidValueType = "INVALID_VALUE_TYPE"
	// ReasonEmptyLikeValue 表示 ~ / !~ 的匹配值为空。
	ReasonEmptyLikeValue = "EMPTY_LIKE_VALUE"
	// ReasonTooManyConditions 表示条件总数超过 [Limits.MaxConditions]。
	ReasonTooManyConditions = "TOO_MANY_CONDITIONS"
	// ReasonIllegalORSyntax 表示 or(...) 语法非法（少于两个条件，或非法的多值分隔）。
	ReasonIllegalORSyntax = "ILLEGAL_OR_SYNTAX"
	// ReasonTooManyInValues 表示 in / notin（以及多值 =）的元素数超过 [Limits.MaxInValues]。
	ReasonTooManyInValues = "TOO_MANY_IN_VALUES"
	// ReasonTooManyJoins 表示 JOIN 数超过 [Limits.MaxJoins]。
	ReasonTooManyJoins = "TOO_MANY_JOINS"
	// ReasonTooManyLikeConditions 表示模糊条件数超过 [Limits.MaxLikeConds]。
	ReasonTooManyLikeConditions = "TOO_MANY_LIKE_CONDITIONS"
	// ReasonFieldNotLikeable: 该列未声明 Likeable，不允许 ~ / !~。
	ReasonFieldNotLikeable = "FIELD_NOT_LIKEABLE"
	// ReasonFieldNotIndexed 已弃用：它过去被用于「不允许 LIKE」，
	// 与名字含义（未建索引）不符。保留常量仅供旧调用方编译通过，新代码用 ReasonFieldNotLikeable。
	ReasonFieldNotIndexed = "FIELD_NOT_INDEXED"
	// ReasonInvalidSortField 表示 sortBy 不在白名单内或无法解析为可排序列。
	ReasonInvalidSortField = "INVALID_SORT_FIELD"
	// ReasonDistinctRelationSort: 一对多 JOIN 需要 DISTINCT 时，不能按关联列排序
	// （PG 要求 ORDER BY 表达式出现在 SELECT 列表；MySQL 语义也不确定）。
	ReasonDistinctRelationSort = "DISTINCT_RELATION_SORT_UNSUPPORTED"
	// ReasonCursorNeedsPK 表示游标分页要求模型有单一主键。
	ReasonCursorNeedsPK = "CURSOR_NEEDS_PRIMARY_KEY"
	// ReasonCursorRelationSort 表示游标分页不支持按关联列排序。
	ReasonCursorRelationSort = "CURSOR_RELATION_SORT_UNSUPPORTED"
	// ReasonInvalidCursor: 游标内容非法（非标量排序值、无法解码等）。
	ReasonInvalidCursor = "INVALID_CURSOR"
	// ReasonInvalidCursorPK: 游标里的主键无法转换为模型中主键的类型。
	ReasonInvalidCursorPK = "INVALID_CURSOR_PK"
)

// InvalidQueryCondition 为 q= 解析/校验失败返回 HTTP 400。
func InvalidQueryCondition(rawQ, reason string) *fxerrors.Error {
	return fxerrors.BadRequest("查询条件解析失败").WithDetails(map[string]any{
		"code":   "INVALID_QUERY_CONDITION",
		"raw_q":  rawQ,
		"reason": reason,
	})
}
