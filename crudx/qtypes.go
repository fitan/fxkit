package crudx

// Operator is a ZStack q= condition operator.
type Operator int

const (
	OpEqual Operator = iota
	OpNotEqual
	OpLike
	OpNotLike
	OpGT
	OpGTE
	OpLT
	OpLTE
	OpIn
	OpNotIn
	OpIsNull
	OpNotNull
)

// FieldKind is the expected type of a filterable column.
type FieldKind int

const (
	FieldString FieldKind = iota
	FieldNumber
	FieldBool
	FieldTime // RFC3339 string or unix seconds (int64)
)

// ConditionGroup is a set of conditions combined with AND, or OR when Or is true.
type ConditionGroup struct {
	Or    bool
	Conds []Condition
}

// FieldSpec describes a filterable column on the root resource or a relation.
type FieldSpec struct {
	Column  string
	Kind    FieldKind
	Indexed bool // required for ~ / !~
}

// RelationSpec describes a joinable association for dotted field paths.
type RelationSpec struct {
	Table  string
	Alias  string
	On     string
	Fields map[string]FieldSpec
	Nested map[string]*RelationSpec
}

// ListSpec is the filter/sort whitelist for [List] / [ApplyList].
// Only names listed here may appear in q= or sortBy.
type ListSpec struct {
	Fields      map[string]FieldSpec
	Relations   map[string]*RelationSpec
	SortFields  map[string]string // API path -> SQL column/expression
	DefaultSort string
	Limits      Limits
}

// Condition is a parsed q= expression.
type Condition struct {
	Field  string
	Op     Operator
	Values []any
	RawQ   string
}

// resolvedField holds SQL column and joins needed for a field path.
type resolvedField struct {
	Column string
	Joins  []joinPlan
}

type joinPlan struct {
	Alias string
	SQL   string // full LEFT JOIN ... ON ...
}
