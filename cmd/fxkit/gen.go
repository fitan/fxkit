package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fitan/fxkit/crudx"
	"github.com/spf13/cobra"
	"gorm.io/gorm/schema"
)

// gormNaming 与 gormx 建连时使用的 GORM 默认命名策略一致。
// 表名/列名必须由它推导，绝不自己造规则：自造的 toSnake/plural 会把
// Person 推导成 persons（GORM 是 people）、APIKey 推导成 a_p_i_key（GORM 是 api_key），
// 生成的代码能编译，但运行期 SQL 找不到列。
var gormNaming = schema.NamingStrategy{IdentifierMaxLength: 64}

// genCmd 是代码生成器的父命令。当前包含 `resource` 与 `client`。
//
//	fxkit gen resource Article --field title:string:required --field body:text
//	fxkit gen client --spec openapi.yaml --out ./internal/clients/users
func genCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Generate code (resources, clients, ...)",
	}
	cmd.AddCommand(genResourceCmd())
	cmd.AddCommand(genClientCmd())
	return cmd
}

// resourceField 是一个 --field flag 的解析结果。
type resourceField struct {
	Name     string // Go 字段名（PascalCase）
	JSONName string // json tag 名（camelCase）
	Column   string // 数据库列名，由 GORM NamingStrategy 推导
	GoType   string // Go 类型字面量
	GormTag  string // gorm:"..."（由固定的标记片段拼成）
	Tags     string // 额外 tag / 校验提示
	Required bool
	Unique   bool
	Indexed  bool
	Optional bool
}

// resourceTemplate 拼接生成的模块源码。
type resourceTemplate struct {
	Package      string // Go 包名（由表名推导）
	TypeName     string // PascalCase 结构体名
	Table        string // GORM 表名
	Path         string // URL 路径（/articles）
	Tag          string // OpenAPI tag（Article）
	Fields       []resourceField
	SearchOn     []string    // 可搜索列（已归一为列名）
	FilterOn     []string    // 可过滤列（已归一为列名）
	SortBy       string      // 默认 ORDER BY（"created_at desc"）
	SortFields   []sortField // List 的 ORDER BY 白名单（去重）
	ListFields   []specColumn
	NilClientMsg string // NewService 里 nil client 的错误文案
}

// sortField 是 ListSpec.SortFields 的一项。
type sortField struct {
	Name   string // 列名，作为 map key
	Column string // 表限定列名（table.column）
}

// specColumn 是生成 ListSpec 时的一列。
type specColumn struct {
	Name     string // 列名，作为 map key
	Column   string // 表限定列名（table.column）
	Kind     string // crudx.FieldString / FieldNumber / FieldBool / FieldTime
	Indexed  bool   // 该列在数据库上有索引
	Like     bool   // 允许 ~ / !~ 模糊查询（只有 --search 指定的列）
	Sortable bool
}

func genResourceCmd() *cobra.Command {
	var (
		fieldsRaw []string
		outDir    string
		table     string
		search    []string
		filters   []string
		sortSpec  string
	)

	cmd := &cobra.Command{
		Use:   "resource <name>",
		Short: "Generate a Huma resource with crudx list (q=/cursor) and explicit write methods",
		Long: `Generate a resource module: list via crudx.List (ZStack-style q= / cursor),
plus explicit Get/Create/Update/Delete mounted by RegisterResource.

Field syntax: <name>:<type>[:flag,flag,...]
  types:   string | text | int | int32 | int64 | float | bool | time
  flags:   required | unique | index | optional

Table and column names come from GORM's NamingStrategy, i.e. exactly what
gormx will use at runtime (Person -> people, APIKey -> api_key). Use --table
to override the table name when the derived one is not what you want.

--search/--filter/--sort must reference real columns (fields, or the built-in
id/created_at/updated_at); otherwise generation fails instead of emitting SQL
that errors at runtime.

Example:
  fxkit gen resource Article \
      --field title:string:required \
      --field body:text \
      --field author_id:int64:index \
      --field published_at:time:optional \
      --search title,body \
      --filter author_id \
      --sort 'created_at desc'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			data, err := parseResourceArgs(name, table, fieldsRaw, search, filters, sortSpec)
			if err != nil {
				return err
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			moduleRoot, _, err := findRepoRoot(cwd)
			if err != nil {
				return err
			}

			target := outDir
			if target == "" {
				target = filepath.Join("internal", data.Table)
			}
			// 输出目录必须落在当前 module 内：`fxkit gen resource ../pwn` 不能
			// 在上层目录里凭空建包（那里的 go.mod/import path 都是别人的）。
			if _, err := relativeToModule(moduleRoot, target); err != nil {
				return err
			}
			absOut, err := filepath.Abs(target)
			if err != nil {
				return err
			}
			outFile := filepath.Join(absOut, data.Package+".go")
			if _, err := os.Stat(outFile); err == nil {
				return fmt.Errorf("refusing to overwrite existing file %q", outFile)
			}

			formatted, err := renderResource(data)
			if err != nil {
				// 不要把整份源码打到 stderr：错误信息里已经带上是什么失败了。
				return err
			}
			if err := os.MkdirAll(absOut, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", absOut, err)
			}
			if err := writeFileAtomic(outFile, formatted, 0o644); err != nil {
				return err
			}

			fmt.Printf("✓ generated %s\n", outFile)
			fmt.Println("Next steps:")
			fmt.Println("  - import the new module from your service's cmd/main.go (ensure huma.Module is in the app graph)")
			fmt.Println("  - run `go mod tidy && go build ./...`")
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&fieldsRaw, "field", nil, "field declaration (repeatable): name:type[:flags]")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default: internal/<gorm table name>)")
	cmd.Flags().StringVar(&table, "table", "", "override the GORM-derived table name (also used for the package name)")
	cmd.Flags().StringSliceVar(&search, "search", nil, "comma-separated columns allowing q=~ fuzzy match (must be indexed)")
	cmd.Flags().StringSliceVar(&filters, "filter", nil, "comma-separated columns allowing q= exact / in filters")
	cmd.Flags().StringVar(&sortSpec, "sort", "created_at desc", "default ORDER BY clause for List")
	return cmd
}

func parseResourceArgs(name, tableOverride string, fields, search, filters []string, sortSpec string) (*resourceTemplate, error) {
	if err := validateName("resource", name); err != nil {
		return nil, err
	}
	typeName := toPascal(name)
	if err := validateGoIdentifier(typeName); err != nil {
		return nil, fmt.Errorf("resource name %q: %w", name, err)
	}

	parsed := make([]resourceField, 0, len(fields))
	seenName := make(map[string]string, len(fields))
	seenJSON := make(map[string]string, len(fields))
	for _, raw := range fields {
		f, err := parseField(raw)
		if err != nil {
			return nil, fmt.Errorf("parse field %q: %w", raw, err)
		}
		if isReservedFieldName(f.Name) {
			return nil, fmt.Errorf("field %q is reserved: generated models already define ID/CreatedAt/UpdatedAt", raw)
		}
		key := strings.ToLower(f.Name)
		if prev, ok := seenName[key]; ok {
			return nil, fmt.Errorf("duplicate field %q (conflicts with %q)", f.Name, prev)
		}
		seenName[key] = f.Name
		if prev, ok := seenJSON[strings.ToLower(f.JSONName)]; ok {
			return nil, fmt.Errorf("field %q maps to json name %q, already used by %q", f.Name, f.JSONName, prev)
		}
		seenJSON[strings.ToLower(f.JSONName)] = f.Name
		parsed = append(parsed, f)
	}

	data := &resourceTemplate{
		TypeName: typeName,
		Tag:      typeName,
		Fields:   parsed,
	}
	if len(parsed) == 0 {
		// 没有业务字段的 model 既没用，也会让生成的 Update 出现未使用的变量。
		return nil, fmt.Errorf("at least one --field is required (e.g. --field title:string:required)")
	}
	if err := data.resolveTable(tableOverride); err != nil {
		return nil, err
	}

	var err error
	if data.SearchOn, err = data.resolveColumns("--search", search); err != nil {
		return nil, err
	}
	if data.FilterOn, err = data.resolveColumns("--filter", filters); err != nil {
		return nil, err
	}
	if data.SortBy, err = data.resolveSort(sortSpec); err != nil {
		return nil, err
	}

	data.ensureSearchIndexes()
	data.buildListSpec()

	if data.Package == "" || data.Path == "" || data.NilClientMsg == "" {
		return nil, fmt.Errorf("internal error: incomplete resource template for %q", name)
	}
	return data, nil
}

// resolveTable 用 GORM 的命名策略推导表名/列名，并校验包名可用。
func (data *resourceTemplate) resolveTable(tableOverride string) error {
	table := gormNaming.TableName(data.TypeName)
	if tableOverride != "" {
		if err := validateTableName(tableOverride); err != nil {
			return err
		}
		table = tableOverride
	}
	if table == "" {
		return fmt.Errorf("cannot derive a table name for %s; pass --table", data.TypeName)
	}

	seen := make(map[string]string, len(data.Fields))
	for i := range data.Fields {
		f := &data.Fields[i]
		f.Column = gormNaming.ColumnName(table, f.Name)
		if f.Column == "" {
			return fmt.Errorf("cannot derive a column name for field %s; use a different field name", f.Name)
		}
		if prev, ok := seen[f.Column]; ok {
			return fmt.Errorf("fields %q and %q both map to column %q; rename one of them", prev, f.Name, f.Column)
		}
		seen[f.Column] = f.Name
	}

	pkg := table
	if err := validateGoPackageName(pkg); err != nil {
		return fmt.Errorf("table %q is not usable as a Go package name: %w (pass --table or --out)", table, err)
	}

	data.Table = table
	data.Package = pkg
	data.Path = "/" + table
	data.NilClientMsg = pkg + ": nil gormx client"
	return nil
}

var tableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateTableName(table string) error {
	if !tableNameRE.MatchString(table) {
		return fmt.Errorf("invalid --table %q: must match %s", table, tableNameRE.String())
	}
	return nil
}

var goIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateGoIdentifier(name string) error {
	if !goIdentifierRE.MatchString(name) {
		return fmt.Errorf("%q is not a valid Go identifier", name)
	}
	if goKeywords[name] {
		return fmt.Errorf("%q is a Go keyword", name)
	}
	return nil
}

// normalizeIdent 把 id / ID / created_at / createdAt 归一为同一种写法。
func normalizeIdent(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", ""))
}

// isReservedFieldName 报告字段名是否与生成的 model 内置字段冲突。
func isReservedFieldName(name string) bool {
	switch normalizeIdent(name) {
	case "id", "createdat", "updatedat":
		return true
	}
	return false
}

// columnOf 把一个用户输入（列名 / Go 字段名 / JSON 名）解析为列名。
func (data *resourceTemplate) columnOf(name string) (string, bool) {
	want := strings.TrimSpace(name)
	if want == "" {
		return "", false
	}
	switch normalizeIdent(want) {
	case "id":
		return "id", true
	case "createdat":
		return "created_at", true
	case "updatedat":
		return "updated_at", true
	}
	for i := range data.Fields {
		f := &data.Fields[i]
		if strings.EqualFold(f.Column, want) || strings.EqualFold(f.Name, want) || strings.EqualFold(f.JSONName, want) {
			return f.Column, true
		}
	}
	return "", false
}

// availableColumns 用于报错提示。
func (data *resourceTemplate) availableColumns() []string {
	cols := make([]string, 0, len(data.Fields)+3)
	cols = append(cols, "id", "created_at", "updated_at")
	for _, f := range data.Fields {
		cols = append(cols, f.Column)
	}
	return cols
}

func (data *resourceTemplate) resolveColumns(flag string, names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		col, ok := data.columnOf(name)
		if !ok {
			return nil, fmt.Errorf("%s: %q is not a column of %s (available: %s)",
				flag, name, data.Table, strings.Join(data.availableColumns(), ", "))
		}
		if seen[col] {
			continue
		}
		seen[col] = true
		out = append(out, col)
	}
	return out, nil
}

// resolveSort 校验 --sort 引用的列，并归一为 crudx 认识的 "col dir" 形式。
func (data *resourceTemplate) resolveSort(spec string) (string, error) {
	if strings.TrimSpace(spec) == "" {
		return "created_at desc", nil
	}
	// 复用运行期的解析逻辑，保证生成值与 crudx.List 的语义完全一致。
	field, dir := crudx.ParseSort(spec)
	if field == "" {
		return "created_at desc", nil
	}
	col, ok := data.columnOf(field)
	if !ok {
		return "", fmt.Errorf("--sort: %q is not a column of %s (available: %s)",
			field, data.Table, strings.Join(data.availableColumns(), ", "))
	}
	return col + " " + dir, nil
}

func fieldByColumn(fields []resourceField, column string) *resourceField {
	for i := range fields {
		if fields[i].Column == column {
			return &fields[i]
		}
	}
	return nil
}

// ensureSearchIndexes 为 --search 指定的列添加 GORM 索引，使 q=~ 合法
// （crudx 要求 Indexed）。TEXT 列会被跳过：MySQL 无法在没有前缀长度的
// 情况下为它们建索引。
func (data *resourceTemplate) ensureSearchIndexes() {
	for _, col := range data.SearchOn {
		f := fieldByColumn(data.Fields, col)
		if f == nil || f.Indexed || f.Unique {
			continue
		}
		if strings.Contains(f.GormTag, "type:text") {
			continue
		}
		f.Indexed = true
		f.GormTag = appendGormTag(f.GormTag, "index")
	}
}

func (data *resourceTemplate) buildListSpec() {
	addField := func(name, kind string, indexed bool) {
		if name == "" {
			return
		}
		for i := range data.ListFields {
			if data.ListFields[i].Name == name {
				if indexed {
					data.ListFields[i].Indexed = true
				}
				return
			}
		}
		data.ListFields = append(data.ListFields, specColumn{
			Name:    name,
			Column:  data.Table + "." + name,
			Kind:    kind,
			Indexed: indexed,
		})
	}
	// markLike 把某列标为「允许 ~ 模糊查询」。只有 --search 指定的列才会。
	markLike := func(name string) {
		for i := range data.ListFields {
			if data.ListFields[i].Name == name {
				data.ListFields[i].Like = true
				return
			}
		}
	}
	addSort := func(name string) {
		if name == "" {
			return
		}
		for _, s := range data.SortFields {
			if s.Name == name {
				return
			}
		}
		data.SortFields = append(data.SortFields, sortField{
			Name:   name,
			Column: data.Table + "." + name,
		})
		// 同步把对应列标为可排序，供模板生成 .Sort()
		for i := range data.ListFields {
			if data.ListFields[i].Name == name {
				data.ListFields[i].Sortable = true
				return
			}
		}
	}

	addField("id", "crudx.FieldNumber", false)
	addField("created_at", "crudx.FieldTime", false)
	addField("updated_at", "crudx.FieldTime", false)
	for _, col := range data.SearchOn {
		kind := "crudx.FieldString"
		if f := fieldByColumn(data.Fields, col); f != nil {
			kind = fieldKindLiteral(f.GoType)
		}
		addField(col, kind, true)
	}
	for _, col := range data.FilterOn {
		kind := "crudx.FieldString"
		indexed := false
		if f := fieldByColumn(data.Fields, col); f != nil {
			kind = fieldKindLiteral(f.GoType)
			indexed = f.Indexed
		}
		addField(col, kind, indexed)
	}

	addSort("id")
	addSort("created_at")
	addSort("updated_at")
	addSort(sortFieldFromSpec(data.SortBy))
	for _, col := range data.SearchOn {
		addSort(col)
		markLike(col)
	}
	for _, col := range data.FilterOn {
		addSort(col)
	}

	// 收敛一次：无论 addSort/addField 的调用先后，都保证 Sortable 与 SortFields 一致。
	sortSet := make(map[string]struct{}, len(data.SortFields))
	for _, s := range data.SortFields {
		sortSet[s.Name] = struct{}{}
	}
	for i := range data.ListFields {
		_, data.ListFields[i].Sortable = sortSet[data.ListFields[i].Name]
	}
}

func sortFieldFromSpec(spec string) string {
	field, _ := crudx.ParseSort(spec)
	return field
}

func fieldKindLiteral(goType string) string {
	switch goType {
	case "int", "int32", "int64", "float64":
		return "crudx.FieldNumber"
	case "bool":
		return "crudx.FieldBool"
	case "time.Time":
		return "crudx.FieldTime"
	default:
		return "crudx.FieldString"
	}
}

var fieldNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func parseField(raw string) (resourceField, error) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) < 2 {
		return resourceField{}, fmt.Errorf("expected name:type[:flags]")
	}
	name := parts[0]
	typ := parts[1]
	flags := ""
	if len(parts) == 3 {
		flags = parts[2]
	}

	if !fieldNameRE.MatchString(name) {
		return resourceField{}, fmt.Errorf("field name %q must match %s", name, fieldNameRE.String())
	}
	goName := toPascal(name)
	if err := validateGoIdentifier(goName); err != nil {
		return resourceField{}, fmt.Errorf("field name %q: %w", name, err)
	}

	f := resourceField{
		Name:     goName,
		JSONName: toCamel(name),
	}

	switch typ {
	case "string":
		f.GoType = "string"
		f.GormTag = "size:255"
	case "text":
		f.GoType = "string"
		f.GormTag = "type:text"
	case "int":
		f.GoType = "int"
	case "int32":
		f.GoType = "int32"
	case "int64":
		f.GoType = "int64"
	case "float", "float64":
		f.GoType = "float64"
	case "bool":
		f.GoType = "bool"
	case "time":
		f.GoType = "time.Time"
	default:
		return f, fmt.Errorf("unsupported type %q (expected string|text|int|int32|int64|float|bool|time)", typ)
	}

	for _, flag := range splitCSV(flags) {
		switch flag {
		case "required":
			f.Required = true
			f.GormTag = appendGormTag(f.GormTag, "not null")
		case "unique":
			f.Unique = true
			f.GormTag = appendGormTag(f.GormTag, "uniqueIndex")
		case "index":
			f.Indexed = true
			f.GormTag = appendGormTag(f.GormTag, "index")
		case "optional":
			f.Optional = true
		default:
			return f, fmt.Errorf("unknown flag %q", flag)
		}
	}

	// 构建 struct 字段 tag
	extraTags := []string{}
	if f.Required {
		extraTags = append(extraTags, `required:"true"`)
	}
	if len(extraTags) > 0 {
		f.Tags = " " + strings.Join(extraTags, " ")
	}

	return f, nil
}

func appendGormTag(tag, addition string) string {
	if tag == "" {
		return addition
	}
	return tag + ";" + addition
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func toPascal(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' })
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

func toCamel(s string) string {
	p := toPascal(s)
	if p == "" {
		return p
	}
	// 只把「开头的连续大写」转小写；若该串后面紧跟小写字母，保留最后一个大写
	// （APIKey -> apiKey），否则全部转小写（URL -> url）。
	i := 0
	for i < len(p) && p[i] >= 'A' && p[i] <= 'Z' {
		i++
	}
	switch {
	case i == 0:
		return p
	case i == len(p):
		return strings.ToLower(p)
	case i == 1:
		return strings.ToLower(p[:1]) + p[1:]
	case p[i] >= 'a' && p[i] <= 'z':
		return strings.ToLower(p[:i-1]) + p[i-1:]
	default:
		return p
	}
}
