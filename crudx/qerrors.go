package crudx

import "github.com/fitan/fxkit/fxerrors"

// Query error reason codes (ZStack-style q= parsing).
const (
	ReasonNoOperator              = "NO_OPERATOR"
	ReasonFieldNotExist           = "FIELD_NOT_EXIST"
	ReasonRelationDepthOverLimit  = "RELATION_DEPTH_OVER_LIMIT"
	ReasonInvalidValueType        = "INVALID_VALUE_TYPE"
	ReasonEmptyLikeValue          = "EMPTY_LIKE_VALUE"
	ReasonTooManyConditions       = "TOO_MANY_CONDITIONS"
	ReasonIllegalORSyntax         = "ILLEGAL_OR_SYNTAX"
	ReasonURLDecodeFailed         = "URL_DECODE_FAILED"
	ReasonTooManyInValues         = "TOO_MANY_IN_VALUES"
	ReasonTooManyJoins            = "TOO_MANY_JOINS"
	ReasonTooManyLikeConditions   = "TOO_MANY_LIKE_CONDITIONS"
	ReasonFieldNotIndexed         = "FIELD_NOT_INDEXED"
	ReasonInvalidSortField        = "INVALID_SORT_FIELD"
)

// InvalidQueryCondition returns HTTP 400 for q= parse/validation failures.
func InvalidQueryCondition(rawQ, reason string) *fxerrors.Error {
	return fxerrors.BadRequest("查询条件解析失败").WithDetails(map[string]any{
		"code":   "INVALID_QUERY_CONDITION",
		"raw_q":  rawQ,
		"reason": reason,
	})
}
