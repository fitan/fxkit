package crudx

// Operator 是 ZStack q= 条件操作符。
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

// FieldKind 是可过滤列的预期类型。
type FieldKind int

const (
	FieldString FieldKind = iota
	FieldNumber
	FieldBool
	FieldTime // RFC3339 字符串或 unix 秒（int64）
)

// ConditionGroup 是一组条件，默认用 AND 组合；Or 为 true 时用 OR 组合。
type ConditionGroup struct {
	Or    bool
	Conds []Condition
}

// FieldSpec 描述根资源或关联上的可过滤列。
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

// RelationSpec 描述点号字段路径可 JOIN 的关联。
type RelationSpec struct {
	Table  string
	Alias  string
	On     string
	Fields map[string]FieldSpec
	Nested map[string]*RelationSpec
	// Cardinality 未设置时保持旧行为（总是 DISTINCT）。
	Cardinality RelationCardinality
}

// ListSpec 是 [List] / [ApplyList] 的过滤/排序白名单。
// 只有此处列出的名称才能出现在 q= 中；sortBy 可用的名称是
// SortFields ∪ Fields ∪ Relations 路径（例如 cluster.name）。
//
// SortFields 只影响 sortBy，不影响 q=；它主要用于两种情况：
// 排序键与过滤键不同名，或排序表达式无法写成 FieldSpec.Column。
type ListSpec struct {
	Fields      map[string]FieldSpec
	Relations   map[string]*RelationSpec
	SortFields  map[string]string // API 路径 -> SQL 列/表达式
	DefaultSort string
	Limits      Limits
	// StrictSort 为 true 时，只有 SortFields 里的名字可以出现在 sortBy。
	//
	// 默认 false：保持「Fields 声明的列都可排序」（含 Relations 点号路径），
	// 兼容既有服务。若要确保「只有显式列入白名单的列才能排序」
	// （避免对 TEXT/未索引列做 ORDER BY），开启它。
	StrictSort bool
}

// Condition 是解析后的 q= 表达式。
type Condition struct {
	Field  string
	Op     Operator
	Values []any
	RawQ   string
}

// resolvedField 保存字段路径所需的 SQL 列与 JOIN。
type resolvedField struct {
	Column string
	Joins  []joinPlan
}

type joinPlan struct {
	Alias       string
	SQL         string // 完整的 LEFT JOIN ... ON ...
	Cardinality RelationCardinality
}
