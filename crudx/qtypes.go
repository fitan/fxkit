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
//
// Indexed 与 Likeable 是**两个独立概念**：
//
//   - Indexed  该列在数据库上有索引（文档与性能提示）
//   - Likeable 允许该列出现在 ~ / !~ 模糊查询里
//
// 拆分的原因：过去只有一个 Indexed 兼任二者，导致「有索引的数值列」
// （如 author_id）被迫允许字符串模糊匹配，语义荒谬且破坏安全边界。
//
// 注意：这里**不做** `Indexed => Likeable` 的向后兼容 —— 那会让新写法失效。
// 需要 LIKE 的列必须显式写 `Likeable: true`。
type FieldSpec struct {
	Column   string
	Kind     FieldKind
	Indexed  bool // 数据库上是否有索引
	Likeable bool // 是否允许 ~ / !~
}

// allowLike 报告该字段是否允许模糊查询。
func (fs FieldSpec) allowLike() bool {
	return fs.Likeable
}

// RelationCardinality 描述关联的基数，用于决定 JOIN 后是否需要 DISTINCT 去重。
type RelationCardinality int

const (
	// RelationUnknown 未声明：保守地使用 DISTINCT（与旧行为一致）。
	RelationUnknown RelationCardinality = iota
	// RelationToOne 多对一 / 一对一：JOIN 不会放大行数，可跳过 DISTINCT。
	RelationToOne
	// RelationToMany 一对多：JOIN 会放大行数，必须 DISTINCT。
	RelationToMany
)

// RelationSpec describes a joinable association for dotted field paths.
type RelationSpec struct {
	Table  string
	Alias  string
	On     string
	Fields map[string]FieldSpec
	Nested map[string]*RelationSpec
	// Cardinality 未设置时保持旧行为（总是 DISTINCT）。
	Cardinality RelationCardinality
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
	Alias       string
	SQL         string // full LEFT JOIN ... ON ...
	Cardinality RelationCardinality
}
