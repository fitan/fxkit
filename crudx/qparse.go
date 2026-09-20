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

// Longer and more specific operators must appear first.
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

// CleanHTTPQValues trims and removes empty elements from already decoded query parameter slices.
func CleanHTTPQValues(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

// ParseHTTPQValues safely decodes raw q query values.
// If an element contains percent-encoded escape sequences, it unescapes them.
// If it is already unescaped plain text, it preserves the literal string.
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
				if err := validateCondition(spec, lim, &c); err != nil {
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
	bestIdx := -1
	var bestTok opToken

	for _, tok := range opTokens {
		idx := strings.Index(expr, tok.suffix)
		if idx <= 0 {
			// idx < 0: not found; idx == 0: field would be empty
			continue
		}
		// The earliest operator in the expression is the one that divides the field from the value.
		// When two operators start at the exact same index (e.g. ">=" vs ">", or ".notin:" vs ".in:"),
		// the one appearing earlier in opTokens is more specific and takes precedence.
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
	// Quoted strings: "hello", 'hello'
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) || (strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		if len(s) >= 2 {
			return s[1 : len(s)-1], nil
		}
	}
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

func validateValueTypes(c *Condition, fs FieldSpec) error {
	if c.Op == OpIsNull || c.Op == OpNotNull {
		return nil
	}
	effectiveOp := c.Op
	if c.Op == OpEqual && len(c.Values) > 1 {
		effectiveOp = OpIn
	}
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
			switch x := v.(type) {
			case string:
				return nil
			case int64:
				c.Values[idx] = strconv.FormatInt(x, 10)
				return nil
			case float64:
				c.Values[idx] = strconv.FormatFloat(x, 'f', -1, 64)
				return nil
			case bool:
				c.Values[idx] = strconv.FormatBool(x)
				return nil
			default:
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
				switch x := v.(type) {
				case string:
					return nil
				case int64:
					c.Values[idx] = strconv.FormatInt(x, 10)
					return nil
				case float64:
					c.Values[idx] = strconv.FormatFloat(x, 'f', -1, 64)
					return nil
				case bool:
					c.Values[idx] = strconv.FormatBool(x)
					return nil
				default:
					return errInvalid
				}
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
	if len(parts) == 3 {
		f, ok := nested.Fields[parts[2]]
		if !ok {
			return resolvedField{}, errInvalid
		}
		return resolvedField{Column: f.Column, Joins: []joinPlan{join, join2}}, nil
	}
	return resolvedField{}, errInvalid
}

// NormalizeEqualToIn converts = with multiple values to OpIn for SQL building.
func NormalizeEqualToIn(c Condition) Condition {
	if c.Op == OpEqual && len(c.Values) > 1 {
		c.Op = OpIn
	}
	return c
}
