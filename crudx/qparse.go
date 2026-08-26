package crudx

import (
	"net/url"
	"strconv"
	"strings"
)

const maxFieldDepth = 3

type opToken struct {
	suffix string
	op     Operator
}

// Longer operators must appear first.
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

// ParseHTTPQValues URL-decodes each raw q query value.
func ParseHTTPQValues(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		decoded, err := url.QueryUnescape(r)
		if err != nil {
			return nil, InvalidQueryCondition(r, ReasonURLDecodeFailed)
		}
		out = append(out, decoded)
	}
	return out, nil
}

// ParseQConditions parses and validates all q= conditions against spec (AND only).
// Prefer [ParseQGroups] when or(...) groups are needed.
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

// ParseQGroups parses q= values into AND groups and controlled OR groups.
// OR syntax: or(field=a,field2=b) — commas separate conditions inside or(...).
func ParseQGroups(spec ListSpec, rawQs []string) ([]ConditionGroup, error) {
	lim := spec.Limits.normalized()
	groups := make([]ConditionGroup, 0, len(rawQs))
	totalConds := 0
	likeCount := 0

	addCond := func(c Condition, or bool) error {
		if err := validateCondition(spec, lim, c); err != nil {
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
		if or {
			if len(groups) > 0 && groups[len(groups)-1].Or {
				groups[len(groups)-1].Conds = append(groups[len(groups)-1].Conds, c)
				return nil
			}
			groups = append(groups, ConditionGroup{Or: true, Conds: []Condition{c}})
			return nil
		}
		groups = append(groups, ConditionGroup{Or: false, Conds: []Condition{c}})
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
				if err := validateCondition(spec, lim, c); err != nil {
					return nil, err
				}
				totalConds++
				if totalConds > lim.MaxConditions {
					return nil, InvalidQueryCondition("", ReasonTooManyConditions)
				}
				if c.Op == OpLike || c.Op == OpNotLike {
					likeCount++
					if likeCount > lim.MaxLikeConds {
						return nil, InvalidQueryCondition("", ReasonTooManyLikeConditions)
					}
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
		if err := addCond(c, false); err != nil {
			return nil, err
		}
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
	for _, tok := range opTokens {
		idx := strings.Index(expr, tok.suffix)
		if idx < 0 {
			continue
		}
		if tok.op == OpIn || tok.op == OpNotIn {
			field = strings.TrimSpace(expr[:idx])
			return field, tok.op, expr[idx+len(tok.suffix):], nil
		}
		if tok.op == OpIsNull || tok.op == OpNotNull {
			field = strings.TrimSpace(expr[:idx])
			if field == "" {
				continue
			}
			// allow field.isnull or field.isnull:
			rest := expr[idx+len(tok.suffix):]
			return field, tok.op, rest, nil
		}
		field = strings.TrimSpace(expr[:idx])
		if field == "" {
			continue
		}
		return field, tok.op, expr[idx+len(tok.suffix):], nil
	}
	return "", 0, "", InvalidQueryCondition(expr, ReasonNoOperator)
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
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			if p := strings.TrimSpace(s[start:i]); p != "" {
				parts = append(parts, p)
			}
			start = i + 1
		}
	}
	if p := strings.TrimSpace(s[start:]); p != "" {
		parts = append(parts, p)
	}
	return parts
}

func parseLiteral(s string) (any, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "null") {
		return nil, nil
	}
	lower := strings.ToLower(s)
	if lower == "true" {
		return true, nil
	}
	if lower == "false" {
		return false, nil
	}
	if isNumberLiteral(s) {
		if strings.Contains(s, ".") {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, err
			}
			return f, nil
		}
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, err
		}
		return i, nil
	}
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
	}
	return true
}

func validateCondition(spec ListSpec, lim Limits, c Condition) error {
	fs, err := resolveFieldSpec(spec, c.Field)
	if err != nil {
		return InvalidQueryCondition(c.RawQ, ReasonFieldNotExist)
	}
	if err := validateValueTypes(c, fs); err != nil {
		return InvalidQueryCondition(c.RawQ, ReasonInvalidValueType)
	}
	if (c.Op == OpLike || c.Op == OpNotLike) && !fs.Indexed {
		return InvalidQueryCondition(c.RawQ, ReasonFieldNotIndexed)
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

func validateValueTypes(c Condition, fs FieldSpec) error {
	if c.Op == OpIsNull || c.Op == OpNotNull {
		return nil
	}
	effectiveOp := c.Op
	if c.Op == OpEqual && len(c.Values) > 1 {
		effectiveOp = OpIn
	}
	checkOne := func(v any) error {
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
			if _, ok := v.(string); !ok {
				return errInvalid
			}
		case OpGT, OpGTE, OpLT, OpLTE:
			switch fs.Kind {
			case FieldTime:
				return coerceTimeValueOK(v)
			default:
				switch v.(type) {
				case int64, float64:
					return nil
				default:
					return errInvalid
				}
			}
		default:
			switch fs.Kind {
			case FieldNumber:
				switch v.(type) {
				case int64, float64:
					return nil
				default:
					return errInvalid
				}
			case FieldBool:
				if _, ok := v.(bool); !ok {
					return errInvalid
				}
			case FieldString:
				if _, ok := v.(string); !ok {
					return errInvalid
				}
			case FieldTime:
				return coerceTimeValueOK(v)
			}
		}
		return nil
	}
	for _, v := range c.Values {
		if err := checkOne(v); err != nil {
			return err
		}
	}
	return nil
}

func coerceTimeValueOK(v any) error {
	switch x := v.(type) {
	case int64, float64:
		return nil
	case string:
		if x == "" {
			return errInvalid
		}
		// Accept RFC3339 or date-only; ApplyList keeps string/number as-is for SQL drivers.
		return nil
	default:
		return errInvalid
	}
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

// resolveField resolves API field path to SQL column and required joins.
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
		Alias: rel.Alias,
		SQL:   "LEFT JOIN " + rel.Table + " AS " + rel.Alias + " ON " + rel.On,
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
		Alias: nested.Alias,
		SQL:   "LEFT JOIN " + nested.Table + " AS " + nested.Alias + " ON " + nested.On,
	}
	f, ok := nested.Fields[parts[2]]
	if !ok {
		return resolvedField{}, errInvalid
	}
	return resolvedField{Column: f.Column, Joins: []joinPlan{join, join2}}, nil
}

// NormalizeEqualToIn converts = with multiple values to OpIn for SQL building.
func NormalizeEqualToIn(c Condition) Condition {
	if c.Op == OpEqual && len(c.Values) > 1 {
		c.Op = OpIn
	}
	return c
}
