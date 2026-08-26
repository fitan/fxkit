package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// genCmd 是代码生成器的父命令。当前包含 `resource`。
//
//	fxkit gen resource Article --field title:string:required --field body:text
func genCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Generate code (resources, clients, ...)",
	}
	cmd.AddCommand(genResourceCmd())
	return cmd
}

// resourceField 是一个 --field flag 的解析结果。
type resourceField struct {
	Name     string // Go 字段名（PascalCase）
	JSONName string // json tag 名（camelCase）
	Column   string // 数据库列名（snake_case）
	GoType   string // Go 类型字面量
	GormTag  string // gorm:"..."
	Tags     string // 额外 json tag / 校验提示
	Required bool
	Unique   bool
	Indexed  bool
	Optional bool
}

// resourceTemplate 拼接生成的模块源码。
type resourceTemplate struct {
	Package    string
	TypeName   string // PascalCase 结构体名
	Path       string // URL 路径（/articles）
	PathSlug   string // 无 leading slash 的路径（articles）
	Tag        string // OpenAPI tag（Article）
	Fields     []resourceField
	SearchOn   []string // 可搜索列
	FilterOn   []string // 可过滤列
	SortBy     string
	SortFields []string     // List 的 ORDER BY 白名单（去重）
	ListFields []specColumn // ListSpec.Fields（去重）
}

// specColumn 是生成 ListSpec 时的一列。
type specColumn struct {
	Name    string
	Kind    string // crudx.FieldString / FieldNumber / FieldBool / FieldTime
	Indexed bool
}

func genResourceCmd() *cobra.Command {
	var (
		fieldsRaw []string
		outDir    string
		search    []string
		filters   []string
		sort      string
	)

	cmd := &cobra.Command{
		Use:   "resource <name>",
		Short: "Generate a Huma resource with crudx list (q=/cursor) and explicit write methods",
		Long: `Generate a resource module: list via crudx.List (ZStack-style q= / cursor),
plus explicit Get/Create/Update/Delete mounted by RegisterResource.

Field syntax: <name>:<type>[:flag,flag,...]
  types:   string | text | int | int32 | int64 | float | bool | time
  flags:   required | unique | index | optional

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
			data, err := parseResourceArgs(name, fieldsRaw, search, filters, sort)
			if err != nil {
				return err
			}

			if outDir == "" {
				outDir = filepath.Join("internal", strings.ToLower(plural(name)))
			}
			outFile := filepath.Join(outDir, strings.ToLower(name)+".go")

			if _, err := os.Stat(outFile); err == nil {
				return fmt.Errorf("refusing to overwrite existing file %q", outFile)
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", outDir, err)
			}

			formatted, err := renderResource(data)
			if err != nil {
				if len(formatted) > 0 {
					fmt.Fprintln(os.Stderr, string(formatted))
				}
				return err
			}
			if err := os.WriteFile(outFile, formatted, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", outFile, err)
			}

			fmt.Printf("✓ generated %s\n", outFile)
			fmt.Println("Next steps:")
			fmt.Println("  - import the new module from services/<svc>/cmd/main.go (ensure huma.Module is in the app graph)")
			fmt.Println("  - run `go mod tidy && go build ./...`")
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&fieldsRaw, "field", nil, "field declaration (repeatable): name:type[:flags]")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default: internal/<plural-lowercase>)")
	cmd.Flags().StringSliceVar(&search, "search", nil, "comma-separated columns allowing q=~ fuzzy match (must be indexed)")
	cmd.Flags().StringSliceVar(&filters, "filter", nil, "comma-separated columns allowing q= exact / in filters")
	cmd.Flags().StringVar(&sort, "sort", "created_at desc", "default ORDER BY clause for List")
	return cmd
}

func parseResourceArgs(name string, fields, search, filters []string, sort string) (*resourceTemplate, error) {
	data := &resourceTemplate{
		Package:  strings.ToLower(plural(name)),
		TypeName: name,
		Path:     "/" + strings.ToLower(plural(name)),
		PathSlug: strings.ToLower(plural(name)),
		Tag:      name,
		SearchOn: search,
		FilterOn: filters,
		SortBy:   sort,
	}

	for _, raw := range fields {
		f, err := parseField(raw)
		if err != nil {
			return nil, fmt.Errorf("parse field %q: %w", raw, err)
		}
		data.Fields = append(data.Fields, f)
	}

	data.ensureSearchIndexes()
	data.buildListSpec()

	return data, nil
}

func sortFieldFromSpec(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	field := strings.Fields(spec)[0]
	if idx := strings.Index(field, ","); idx >= 0 {
		field = field[:idx]
	}
	return strings.TrimSpace(field)
}

// ensureSearchIndexes adds a GORM index on --search columns so q=~ is legal
// (crudx requires Indexed). TEXT columns are skipped: MySQL cannot index them
// without a prefix length.
func (data *resourceTemplate) ensureSearchIndexes() {
	for _, col := range data.SearchOn {
		f := matchField(data.Fields, col)
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
		name = toSnake(strings.TrimSpace(name))
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
		data.ListFields = append(data.ListFields, specColumn{Name: name, Kind: kind, Indexed: indexed})
	}
	addSort := func(name string) {
		name = toSnake(strings.TrimSpace(name))
		if name == "" {
			return
		}
		for _, s := range data.SortFields {
			if s == name {
				return
			}
		}
		data.SortFields = append(data.SortFields, name)
	}

	addField("id", "crudx.FieldNumber", false)
	addField("created_at", "crudx.FieldTime", false)
	addField("updated_at", "crudx.FieldTime", false)
	for _, col := range data.SearchOn {
		kind := "crudx.FieldString"
		if f := matchField(data.Fields, col); f != nil {
			kind = fieldKindLiteral(f.GoType)
		}
		addField(col, kind, true)
	}
	for _, col := range data.FilterOn {
		kind := "crudx.FieldString"
		indexed := false
		if f := matchField(data.Fields, col); f != nil {
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
	}
	for _, col := range data.FilterOn {
		addSort(col)
	}
}

func matchField(fields []resourceField, name string) *resourceField {
	want := toSnake(strings.TrimSpace(name))
	for i := range fields {
		if fields[i].Column == want || strings.EqualFold(fields[i].JSONName, name) || strings.EqualFold(fields[i].Name, name) {
			return &fields[i]
		}
	}
	return nil
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

	f := resourceField{
		Name:     toPascal(name),
		JSONName: toCamel(name),
		Column:   toSnake(name),
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

// plural 通过追加 "s"（或擦音词尾加 "es"）生成英文复数。
// 代码生成足够用 —— 用户可用 --out / --tag 覆盖包名。
func plural(s string) string {
	if s == "" {
		return s
	}
	low := strings.ToLower(s)
	switch {
	case strings.HasSuffix(low, "s"),
		strings.HasSuffix(low, "x"),
		strings.HasSuffix(low, "z"),
		strings.HasSuffix(low, "ch"),
		strings.HasSuffix(low, "sh"):
		return s + "es"
	case strings.HasSuffix(low, "y") && len(s) > 1 && !isVowel(s[len(s)-2]):
		return s[:len(s)-1] + "ies"
	default:
		return s + "s"
	}
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u', 'A', 'E', 'I', 'O', 'U':
		return true
	}
	return false
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
	return strings.ToLower(p[:1]) + p[1:]
}

func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
