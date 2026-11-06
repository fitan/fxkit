package crudx

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxFieldDepth = 3

type opToken struct {
	suffix string
	op     Operator
}

// 更长、更具体的操作符必须排在前面。
var opTokens = []opToken{
	{".notin:", OpNotIn},
	{".in:", OpIn},
	{".isnull", OpIsNull},
	{".notnull", OpNotNull},
	{">=", OpGTE},
	{"<=", OpLTE},
	{"!=", OpNotEqual},
	{"!~", OpNotLike},
	{"~", OpLike},
	{"=", OpEqual},
	{">", OpGT},
	{"<", OpLT},
}

// ParseHTTPQValues 安全地解码原始 q 查询值。
//
// 只用于拿到**未解码**的原始查询字符串（例如自己解析 RawQuery）。
// 如果值来自 net/url 或 Huma 的 query 绑定，它已经是解码后的，
// 再调本函数会二次解码（"a%20b" 变成 "a b"）。
//
// 若元素包含百分号编码的转义序列，则对其反转义；
// 若元素已是未转义的纯文本，则保留原始字符串。
func ParseHTTPQValues(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !strings.Contains(r, "%") {
			out = append(out, r)
			continue
		}
		decoded, err := url.QueryUnescape(r)
		if err != nil {
			// 字面量 '%'（例如搜索词 "50%"）不应该让整个查询 400；按原文匹配。
			out = append(out, r)
			continue
		}
		out = append(out, decoded)
	}
	return out, nil
}

// ParseQConditions 按 spec 解析并校验所有 q= 条件（仅 AND）。
// 需要 or(...) 分组时优先用 [ParseQGroups]。
func ParseQConditions(spec ListSpec, rawQs []string) ([]Condition, error) {
	groups, err := ParseQGroups(spec, rawQs)
	if err != nil {
		return nil, err
	}
	out := make([]Condition, 0, len(rawQs))
	for _, g := range groups {
		if g.Or {
			return nil, InvalidQueryCondition("", ReasonIllegalORSyntax)
		}
		out = append(out, g.Conds...)
	}
	return out, nil
}

// ParseQGroups 将 q= 值解析为 AND 组与受控 OR 组。
// OR 语法：or(field=a,field2=b) —— 逗号分隔 or(...) 内的各个条件。
func ParseQGroups(spec ListSpec, rawQs []string) ([]ConditionGroup, error) {
	lim := spec.Limits.normalized()
	groups := make([]ConditionGroup, 0, len(rawQs))
	totalConds := 0
	likeCount := 0

	// checkLimits 累计并校验条件数量上限（AND 与 or(...) 共用一套规则）。
	//
	// 早期实现把这段限额逻辑在 or(...) 分支里又写了一遍，
	// 新增限额时漏改一侧就会让 or() 组成为绕过防护的后门。
	checkLimits := func(c Condition) error {
		if err := validateCondition(spec, lim, &c); err != nil {
			return err
		}
		totalConds++
		if totalConds > lim.MaxConditions {
			return InvalidQueryCondition("", ReasonTooManyConditions)
		}
		if c.Op == OpLike || c.Op == OpNotLike {
			likeCount++
			if likeCount > lim.MaxLikeConds {
				return InvalidQueryCondition("", ReasonTooManyLikeConditions)
			}
		}
		return nil
	}

	for _, raw := range rawQs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(raw), "or(") && strings.HasSuffix(raw, ")") {
			inner := raw[len("or(") : len(raw)-1]
			parts := splitList(inner, ',')
			if len(parts) < 2 {
				return nil, InvalidQueryCondition(raw, ReasonIllegalORSyntax)
			}
			var orConds []Condition
			for _, p := range parts {
				c, err := parseOneCondition(p)
				if err != nil {
					return nil, err
				}
				if err := checkLimits(c); err != nil {
					return nil, err
				}
				orConds = append(orConds, c)
			}
			groups = append(groups, ConditionGroup{Or: true, Conds: orConds})
			continue
		}
		c, err := parseOneCondition(raw)
		if err != nil {
			return nil, err
		}
		if err := checkLimits(c); err != nil {
			return nil, err
		}
		groups = append(groups, ConditionGroup{Or: false, Conds: []Condition{c}})
	}
	return groups, nil
}

func parseOneCondition(raw string) (Condition, error) {
	field, op, valueRaw, err := splitFieldOpValue(raw)
	if err != nil {
		return Condition{}, err
	}
	field = strings.TrimSpace(field)
	if field == "" {
		return Condition{}, InvalidQueryCondition(raw, ReasonNoOperator)
	}
	if strings.Count(field, ".") >= maxFieldDepth {
		return Condition{}, InvalidQueryCondition(raw, ReasonRelationDepthOverLimit)
	}

	values, err := parseValues(op, valueRaw, raw)
	if err != nil {
		return Condition{}, err
	}
	return Condition{Field: field, Op: op, Values: values, RawQ: raw}, nil
}

func splitFieldOpValue(expr string) (field string, op Operator, valueRaw string, err error) {
	bestIdx := -1
	var bestTok opToken

	for _, tok := range opTokens {
		idx := strings.Index(expr, tok.suffix)
		if idx <= 0 {
			// idx < 0：未找到；idx == 0：字段会为空
			continue
		}
		// 表达式中位置最靠前的操作符才是分隔字段与值的那个。
		// 当两个操作符起始于同一位置（例如 ">=" 与 ">"，或 ".notin:" 与 ".in:"）时，
		// 在 opTokens 中出现更早的那个更具体，优先生效。
		if bestIdx < 0 || idx < bestIdx {
			bestIdx = idx
			bestTok = tok
		}
	}

	if bestIdx < 0 {
		return "", 0, "", InvalidQueryCondition(expr, ReasonNoOperator)
	}

	field = strings.TrimSpace(expr[:bestIdx])
	valueRaw = expr[bestIdx+len(bestTok.suffix):]
	return field, bestTok.op, valueRaw, nil
}

func parseValues(op Operator, valueRaw, raw string) ([]any, error) {
	valueRaw = strings.TrimSpace(valueRaw)
	switch op {
	case OpIsNull, OpNotNull:
		return nil, nil
	case OpIn, OpNotIn:
		parts := splitList(valueRaw, ',')
		if len(parts) == 0 {
			return nil, InvalidQueryCondition(raw, ReasonInvalidValueType)
		}
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			v, err := parseLiteral(p)
			if err != nil {
				return nil, InvalidQueryCondition(raw, ReasonInvalidValueType)
			}
			out = append(out, v)
		}
		return out, nil
	case OpEqual:
		if strings.Contains(valueRaw, "|") {
			parts := splitList(valueRaw, '|')
			if len(parts) == 0 {
				return nil, InvalidQueryCondition(raw, ReasonInvalidValueType)
			}
			out := make([]any, 0, len(parts))
			for _, p := range parts {
				v, err := parseLiteral(p)
				if err != nil {
					return nil, InvalidQueryCondition(raw, ReasonInvalidValueType)
				}
				out = append(out, v)
			}
			return out, nil
		}
		v, err := parseLiteral(valueRaw)
		if err != nil {
			return nil, InvalidQueryCondition(raw, ReasonInvalidValueType)
		}
		return []any{v}, nil
	default:
		if strings.Contains(valueRaw, "|") {
			return nil, InvalidQueryCondition(raw, ReasonIllegalORSyntax)
		}
		if (op == OpLike || op == OpNotLike) && valueRaw == "" {
			return nil, InvalidQueryCondition(raw, ReasonEmptyLikeValue)
		}
		v, err := parseLiteral(valueRaw)
		if err != nil {
			return nil, InvalidQueryCondition(raw, ReasonInvalidValueType)
		}
		return []any{v}, nil
	}
}

func splitList(s string, sep byte) []string {
	var parts []string
	start := 0
	inSingleQuote := false
	inDoubleQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		default:
			if c == sep && !inSingleQuote && !inDoubleQuote {
				if p := strings.TrimSpace(s[start:i]); p != "" {
					parts = append(parts, p)
				}
				start = i + 1
			}
		}
	}
	if p := strings.TrimSpace(s[start:]); p != "" {
		parts = append(parts, p)
	}
	return parts
}

func parseLiteral(s string) (any, error) {
	s = strings.TrimSpace(s)
	// 带引号的字符串："hello"、'hello'
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) || (strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		if len(s) >= 2 {
			return s[1 : len(s)-1], nil
		}
	}
	if strings.EqualFold(s, "null") {
		return nil, nil
	}
	// 这里刻意保留原始文本：是否转成数值/布尔/时间取决于字段的 FieldSpec.Kind，
	// 在 validateValueTypes 中完成。否则字符串列 "007" 会被数值化成 7，
	// 超长数字也会被误判为非法。
	return s, nil
}

func isNumberLiteral(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '-' || s[0] == '+' {
		i = 1
	}
	if i >= len(s) {
		return false
	}
	dot := false
	hasDigit := false
	for ; i < len(s); i++ {
		if s[i] == '.' {
			if dot {
				return false
			}
			dot = true
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
		hasDigit = true
	}
	return hasDigit
}

func validateCondition(spec ListSpec, lim Limits, c *Condition) error {
	fs, err := resolveFieldSpec(spec, c.Field)
	if err != nil {
		return InvalidQueryCondition(c.RawQ, ReasonFieldNotExist)
	}
	if err := validateValueTypes(c, fs); err != nil {
		return InvalidQueryCondition(c.RawQ, ReasonInvalidValueType)
	}
	if (c.Op == OpLike || c.Op == OpNotLike) && !fs.allowLike() {
		return InvalidQueryCondition(c.RawQ, ReasonFieldNotLikeable)
	}
	maxIn := lim.MaxInValues
	if c.Op == OpEqual && len(c.Values) > 1 {
		if len(c.Values) > maxIn {
			return InvalidQueryCondition(c.RawQ, ReasonTooManyInValues)
		}
	}
	if c.Op == OpIn || c.Op == OpNotIn {
		if len(c.Values) > maxIn {
			return InvalidQueryCondition(c.RawQ, ReasonTooManyInValues)
		}
	}
	return nil
}

func validateValueTypes(c *Condition, fs FieldSpec) error {
	if c.Op == OpIsNull || c.Op == OpNotNull {
		return nil
	}
	effectiveOp := c.Op
	if c.Op == OpEqual && len(c.Values) > 1 {
		effectiveOp = OpIn
	}
	// coerce 按字段 Kind 把 parseLiteral 保留的原始文本转成 SQL 参数。
	checkAndCoerce := func(idx int, v any) error {
		if v == nil {
			switch effectiveOp {
			case OpGT, OpGTE, OpLT, OpLTE:
				return errInvalid
			default:
				return nil
			}
		}
		switch effectiveOp {
		case OpLike, OpNotLike:
			s, ok := stringValue(v)
			if !ok {
				return errInvalid
			}
			c.Values[idx] = s
		case OpGT, OpGTE, OpLT, OpLTE:
			switch fs.Kind {
			case FieldTime:
				return coerceTimeValueOK(v)
			case FieldNumber:
				n, ok := coerceNumber(v)
				if !ok {
					return errInvalid
				}
				c.Values[idx] = n
			case FieldString:
				// 字符串列保留原文做文本比较（例如 name > 'b'）。
				s, ok := stringValue(v)
				if !ok {
					return errInvalid
				}
				c.Values[idx] = s
			default:
				// 布尔的区间比较没有语义，沿用旧行为直接拒绝。
				if _, ok := numericValue(v); !ok {
					return errInvalid
				}
			}
		default:
			switch fs.Kind {
			case FieldNumber:
				n, ok := coerceNumber(v)
				if !ok {
					return errInvalid
				}
				c.Values[idx] = n
			case FieldBool:
				b, ok := coerceBool(v)
				if !ok {
					return errInvalid
				}
				c.Values[idx] = b
			case FieldString:
				s, ok := stringValue(v)
				if !ok {
					return errInvalid
				}
				c.Values[idx] = s
			case FieldTime:
				return coerceTimeValueOK(v)
			}
		}
		return nil
	}
	for i, v := range c.Values {
		if err := checkAndCoerce(i, v); err != nil {
			return err
		}
	}
	return nil
}

func stringValue(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(x), true
	default:
		return "", false
	}
}

func numericValue(v any) (any, bool) {
	switch x := v.(type) {
	case int64, float64:
		return x, true
	default:
		return nil, false
	}
}

func coerceNumber(v any) (any, bool) {
	if n, ok := numericValue(v); ok {
		return n, true
	}
	s, ok := v.(string)
	if !ok || !isNumberLiteral(s) {
		return nil, false
	}
	if strings.Contains(s, ".") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, false
		}
		return f, true
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, false
	}
	return i, true
}

func coerceBool(v any) (any, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		if strings.EqualFold(x, "true") {
			return true, true
		}
		if strings.EqualFold(x, "false") {
			return false, true
		}
	}
	return nil, false
}

func coerceTimeValueOK(v any) error {
	switch x := v.(type) {
	case int64, float64:
		return nil
	case string:
		return validateTimeLiteral(x)
	default:
		return errInvalid
	}
}

// validateTimeLiteral 接受 RFC3339 / 常见 SQL 布局 / 仅日期 / unix 秒。
// ApplyList 会为 SQL 驱动保留原始字符串。
func validateTimeLiteral(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errInvalid
	}
	if isNumberLiteral(s) {
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return nil
		}
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return nil
		}
		return errInvalid
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if _, err := time.Parse(layout, s); err == nil {
			return nil
		}
	}
	return errInvalid
}

var errInvalid = strconv.ErrSyntax

func resolveFieldSpec(spec ListSpec, path string) (FieldSpec, error) {
	parts := strings.Split(path, ".")
	if len(parts) > maxFieldDepth {
		return FieldSpec{}, errInvalid
	}
	if len(parts) == 1 {
		f, ok := spec.Fields[parts[0]]
		if !ok {
			return FieldSpec{}, errInvalid
		}
		return f, nil
	}
	rel, ok := spec.Relations[parts[0]]
	if !ok {
		return FieldSpec{}, errInvalid
	}
	if len(parts) == 2 {
		f, ok := rel.Fields[parts[1]]
		if !ok {
			return FieldSpec{}, errInvalid
		}
		return f, nil
	}
	nested, ok := rel.Nested[parts[1]]
	if !ok {
		return FieldSpec{}, errInvalid
	}
	f, ok := nested.Fields[parts[2]]
	if !ok {
		return FieldSpec{}, errInvalid
	}
	return f, nil
}

// resolveField 将 API 字段路径解析为 SQL 列与所需 JOIN。
func resolveField(spec ListSpec, path string) (resolvedField, error) {
	parts := strings.Split(path, ".")
	if len(parts) > maxFieldDepth {
		return resolvedField{}, errInvalid
	}
	if len(parts) == 1 {
		f, ok := spec.Fields[parts[0]]
		if !ok {
			return resolvedField{}, errInvalid
		}
		return resolvedField{Column: f.Column}, nil
	}
	rel, ok := spec.Relations[parts[0]]
	if !ok {
		return resolvedField{}, errInvalid
	}
	join := joinPlan{
		Alias:       rel.Alias,
		SQL:         "LEFT JOIN " + rel.Table + " AS " + rel.Alias + " ON " + rel.On,
		Cardinality: rel.Cardinality,
	}
	if len(parts) == 2 {
		f, ok := rel.Fields[parts[1]]
		if !ok {
			return resolvedField{}, errInvalid
		}
		return resolvedField{Column: f.Column, Joins: []joinPlan{join}}, nil
	}
	nested, ok := rel.Nested[parts[1]]
	if !ok {
		return resolvedField{}, errInvalid
	}
	join2 := joinPlan{
		Alias:       nested.Alias,
		SQL:         "LEFT JOIN " + nested.Table + " AS " + nested.Alias + " ON " + nested.On,
		Cardinality: nested.Cardinality,
	}
	if len(parts) == 3 {
		f, ok := nested.Fields[parts[2]]
		if !ok {
			return resolvedField{}, errInvalid
		}
		return resolvedField{Column: f.Column, Joins: []joinPlan{join, join2}}, nil
	}
	return resolvedField{}, errInvalid
}

// NormalizeEqualToIn 将多值的 = 转换为 OpIn，以便构建 SQL。
func NormalizeEqualToIn(c Condition) Condition {
	if c.Op == OpEqual && len(c.Values) > 1 {
		c.Op = OpIn
	}
	return c
}
