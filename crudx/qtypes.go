package crudx

// Operator 是 ZStack q= 条件操作符。
type Operator int

const (
	// OpEqual 对应 q= 中的 "="，生成等值比较。
	OpEqual Operator = iota
	// OpNotEqual 对应 "!="，生成 "<>" 比较。
	OpNotEqual
	// OpLike 对应 "~"，生成 LIKE %值% 包含匹配（需 Likeable）。
	OpLike
	// OpNotLike 对应 "!~"，生成 NOT LIKE %值% 排除匹配（需 Likeable）。
	OpNotLike
	// OpGT 对应 ">"，生成大于比较。
	OpGT
	// OpGTE 对应 ">="，生成大于等于比较。
	OpGTE
	// OpLT 对应 "<"，生成小于比较。
	OpLT
	// OpLTE 对应 "<="，生成小于等于比较。
	OpLTE
	// OpIn 对应 ".in:"，生成 IN (...)。
	OpIn
	// OpNotIn 对应 ".notin:"，生成 NOT IN (...)。
	OpNotIn
	// OpIsNull 对应 ".isnull"，生成 IS NULL。
	OpIsNull
	// OpNotNull 对应 ".notnull"，生成 IS NOT NULL。
	OpNotNull
)

// FieldKind 是可过滤列的预期类型。
type FieldKind int

const (
	// FieldString 字符串列；值按文本比较与模糊匹配。
	FieldString FieldKind = iota
	// FieldNumber 数值列；值会被转换为 int64 或 float64。
	FieldNumber
	// FieldBool 布尔列；值接受 bool 或 "true"/"false" 字符串。
	FieldBool
	// FieldTime 时间列；值接受 RFC3339 字符串或 unix 秒（int64）。
	FieldTime
)

// ConditionGroup 是一组条件，默认用 AND 组合；Or 为 true 时用 OR 组合。
type ConditionGroup struct {
	// Or 为 true 时组内条件用 OR 组合，否则用 AND 组合。
	Or bool
	// Conds 组内的条件列表；为空时该组被跳过。
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
	// Column 对应的数据库列名，可带表别名前缀（如 "users.name"）。
	Column string
	// Kind 列的值类型，决定 q= 中的值如何校验与转换。
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
	// Table 关联表名，用于生成 LEFT JOIN <Table> AS <Alias>。
	Table string
	// Alias JOIN 别名，同时是点号路径的第一段（如 "cluster.name" 中的 cluster）。
	Alias string
	// On JOIN 的 ON 条件 SQL 片段。
	On string
	// Fields 该关联上可过滤/排序的字段，键为路径的第二段。
	Fields map[string]FieldSpec
	// Nested 更深一层的关联，键为路径的第二段；字段路径最多三段。
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
	// Fields 根资源可过滤字段白名单，键为 q= 中出现的字段名。
	Fields map[string]FieldSpec
	// Relations 可通过点号路径 JOIN 的关联白名单。
	Relations  map[string]*RelationSpec
	SortFields map[string]string // API 路径 -> SQL 列（仅限标识符）
	// DefaultSort 默认排序，格式如 "created_at desc"；sortBy 为空时使用。
	DefaultSort string
	// Limits 查询保护上限（条件数、IN 元素数、JOIN 数、页大小等）；零值按内置默认填充。
	Limits Limits
	// StrictSort 为 true 时，只有 SortFields 里的名字可以出现在 sortBy。
	//
	// 默认 false：保持「Fields 声明的列都可排序」（含 Relations 点号路径），
	// 兼容既有服务。若要确保「只有显式列入白名单的列才能排序」
	// （避免对 TEXT/未索引列做 ORDER BY），开启它。
	StrictSort bool
}

// Condition 是解析后的 q= 表达式。
type Condition struct {
	// Field 字段路径，根字段或点号关联路径（如 "cluster.name"）。
	Field string
	// Op 比较操作符。
	Op Operator
	// Values 解析后的值列表；IN / NOT IN 为多值，IS NULL / IS NOT NULL 为空，其余通常单值。
	Values []any
	// RawQ 原始 q= 表达式，用于错误消息与错误详情回显。
	RawQ string
}

// resolvedField 保存字段路径所需的 SQL 列与 JOIN。
type resolvedField struct {
	// Column 是字段对应的 SQL 列名，关联字段会带 JOIN 别名前缀。
	Column string
	// Joins 是访问该列所需的 LEFT JOIN，按从外到内的顺序应用。
	Joins []joinPlan
}

// joinPlan 是一条 LEFT JOIN 的生成计划，用于把点号字段路径落到关联表上。
type joinPlan struct {
	// Alias 是 JOIN 别名，同时作为「该关联已 JOIN」的去重键。
	Alias string
	SQL   string // 完整的 LEFT JOIN ... ON ...
	// Cardinality 决定 JOIN 后是否需要按主键去重（一对多会放大行数）。
	Cardinality RelationCardinality
}
