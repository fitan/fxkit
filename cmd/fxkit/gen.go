package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"text/template"

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
	Package   string
	TypeName  string // PascalCase 结构体名
	Path      string // URL 路径（/articles）
	PathSlug  string // 无 leading slash 的路径（articles）
	DaprAppID string // Invoke.Call 的被调 Dapr app id
	Tag       string // OpenAPI tag（Article）
	Fields    []resourceField
	SearchOn   []string // 可搜索列
	FilterOn   []string // 可过滤列
	SortBy     string
	SortFields []string // List 的 ORDER BY 白名单
	UsesTime   bool
}

func genResourceCmd() *cobra.Command {
	var (
		fieldsRaw []string
		outDir    string
		search    []string
		filters   []string
		sort      string
		appID     string
	)

	cmd := &cobra.Command{
		Use:   "resource <name>",
		Short: "Generate typed JSON CRUD (Huma GET list + JSON handlers) backed by GORM",
		Long: `Generate a typed JSON CRUD module: list via Huma GET with ZStack-style q= params;
get/create/update/delete via Huma (add routes as needed).

Field syntax: <name>:<type>[:flag,flag,...]
  types:   string | text | int | int32 | int64 | float | bool | time
  flags:   required | unique | index | optional

Example:
  fxkit gen resource Article \
      --app-id my-service \
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
			data, err := parseResourceArgs(name, fieldsRaw, search, filters, sort, appID)
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

			tmpl, err := template.New("resource").Funcs(template.FuncMap{
				"join": strings.Join,
			}).Parse(resourceTmpl)
			if err != nil {
				return fmt.Errorf("template parse: %w", err)
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, data); err != nil {
				return fmt.Errorf("template execute: %w", err)
			}

			formatted, err := format.Source(buf.Bytes())
			if err != nil {
			// 输出未格式化源码以便调试模板问题。
				fmt.Fprintln(os.Stderr, buf.String())
				return fmt.Errorf("gofmt generated source: %w", err)
			}
			if err := os.WriteFile(outFile, formatted, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", outFile, err)
			}

			fmt.Printf("✓ generated %s\n", outFile)
			fmt.Println("Next steps:")
			fmt.Println("  - import the new module from cmd/main.go (ensure huma.Module is in the app graph)")
			fmt.Println("  - run `go mod tidy && go build ./...`")
			fmt.Println("  - set --app-id to the callee Dapr app id (Invoke.Call); must match dapr.yaml")
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&fieldsRaw, "field", nil, "field declaration (repeatable): name:type[:flags]")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default: internal/<plural-lowercase>)")
	cmd.Flags().StringSliceVar(&search, "search", nil, "comma-separated columns allowing q=~ fuzzy match (must be indexed)")
	cmd.Flags().StringSliceVar(&filters, "filter", nil, "comma-separated columns allowing q= exact / in filters")
	cmd.Flags().StringVar(&sort, "sort", "created_at desc", "default ORDER BY clause for List")
	cmd.Flags().StringVar(&appID, "app-id", "fxkit-example", "Dapr app id of the service that hosts these Invoke methods (Invoke.Call target)")
	return cmd
}

func parseResourceArgs(name string, fields, search, filters []string, sort, appID string) (*resourceTemplate, error) {
	data := &resourceTemplate{
		Package:   strings.ToLower(plural(name)),
		TypeName:  name,
		Path:      "/" + strings.ToLower(plural(name)),
		PathSlug:  strings.ToLower(plural(name)),
		DaprAppID: appID,
		Tag:       name,
		SearchOn:  search,
		FilterOn:  filters,
		SortBy:    sort,
	}

	for _, raw := range fields {
		f, err := parseField(raw)
		if err != nil {
			return nil, fmt.Errorf("parse field %q: %w", raw, err)
		}
		if f.GoType == "time.Time" {
			data.UsesTime = true
		}
		data.Fields = append(data.Fields, f)
	}

	data.SortFields = buildSortFields(sort, search, filters)

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

func buildSortFields(defaultSort string, cols ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	add(sortFieldFromSpec(defaultSort))
	for _, list := range cols {
		for _, c := range list {
			add(c)
		}
	}
	if len(out) == 0 {
		return []string{"id", "created_at"}
	}
	return out
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

// resourceTmpl 是 `fxkit gen resource` 输出的 Go 源码模板。
const resourceTmpl = `// 由 fxkit gen resource 生成；提交后可自由编辑。
package {{.Package}}

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/fxerrors"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/server"
	"go.uber.org/fx"
)

// {{.TypeName}} 是 GORM model。JSON tag 为 "-" 表示永不直接暴露；请调整 List/Detail DTO。
type {{.TypeName}} struct {
	ID int64 ` + "`gorm:\"primaryKey\" json:\"-\"`" + `
{{- range .Fields}}
	{{.Name}} {{.GoType}} ` + "`gorm:\"{{.GormTag}}\" json:\"-\"`" + `
{{- end}}
	CreatedAt time.Time ` + "`json:\"-\"`" + `
	UpdatedAt time.Time ` + "`json:\"-\"`" + `
}

// {{.TypeName}}ListRow 是列表投影（可裁剪字段以隐藏数据）。
type {{.TypeName}}ListRow struct {
	ID int64 ` + "`json:\"id\"`" + `
{{- range .Fields}}
	{{.Name}} {{.GoType}} ` + "`json:\"{{.JSONName}}{{if .Optional}},omitempty{{end}}\"`" + `
{{- end}}
}

// {{.TypeName}}Detail 由 get/create/update 返回。
type {{.TypeName}}Detail struct {
	ID int64 ` + "`json:\"id\"`" + `
{{- range .Fields}}
	{{.Name}} {{.GoType}} ` + "`json:\"{{.JSONName}}{{if .Optional}},omitempty{{end}}\"`" + `
{{- end}}
	CreatedAt time.Time ` + "`json:\"createdAt\"`" + `
	UpdatedAt time.Time ` + "`json:\"updatedAt\"`" + `
}

type List{{.TypeName}}Resp struct {
	Items []{{.TypeName}}ListRow ` + "`json:\"items\"`" + `
	Total *int64                 ` + "`json:\"total,omitempty\"`" + `
	Start int                    ` + "`json:\"start\"`" + `
	Limit int                    ` + "`json:\"limit\"`" + `
}

type Get{{.TypeName}}Req struct {
	ID string ` + "`json:\"id\"`" + `
}

type Create{{.TypeName}}Req struct {
{{- range .Fields}}
	{{.Name}} {{.GoType}} ` + "`json:\"{{.JSONName}}{{if .Optional}},omitempty{{end}}\"`" + `
{{- end}}
}

type Update{{.TypeName}}Req struct {
	ID string ` + "`json:\"id\"`" + `
{{- range .Fields}}
	{{.Name}} {{.GoType}} ` + "`json:\"{{.JSONName}}{{if .Optional}},omitempty{{end}}\"`" + `
{{- end}}
}

type Delete{{.TypeName}}Req struct {
	ID string ` + "`json:\"id\"`" + `
}

type delete{{.TypeName}}Resp struct{}

// Service 实现 JSON CRUD handler。
type Service struct {
	client   *gormx.Client
	listSpec crudx.ListSpec
}

// NewService 解析元数据并在开发环境运行 AutoMigrate。
func NewService(client *gormx.Client, lc fx.Lifecycle) (*Service, error) {
	if client == nil {
		return nil, fxerrors.Internal("{{.Package}}: nil gormx client")
	}
	if _, err := crudx.ResolveModel[{{.TypeName}}](client.Conn(context.Background())); err != nil {
		return nil, err
	}
	s := &Service{
		client: client,
		listSpec: crudx.ListSpec{
			Fields: map[string]crudx.FieldSpec{
				"id":         {Column: "{{.PathSlug}}.id", Kind: crudx.FieldNumber},
				"created_at": {Column: "{{.PathSlug}}.created_at", Kind: crudx.FieldNumber},
				"updated_at": {Column: "{{.PathSlug}}.updated_at", Kind: crudx.FieldNumber},
{{- range .SearchOn}}
				"{{.}}": {Column: "{{$.PathSlug}}.{{.}}", Kind: crudx.FieldString, Indexed: true},
{{- end}}
{{- range .FilterOn}}
				"{{.}}": {Column: "{{$.PathSlug}}.{{.}}", Kind: crudx.FieldString},
{{- end}}
			},
			SortFields: map[string]string{
				"id":         "{{.PathSlug}}.id",
				"created_at": "{{.PathSlug}}.created_at",
				"updated_at": "{{.PathSlug}}.updated_at",
{{- range .SortFields}}
				"{{.}}": "{{$.PathSlug}}.{{.}}",
{{- end}}
			},
			DefaultSort: "{{.SortBy}}",
			Limits:      crudx.Limits{LimitDefault: 20, LimitMax: 100},
		},
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var z {{.TypeName}}
			return client.Conn(ctx).AutoMigrate(&z)
		},
	})
	return s, nil
}

// Routes 挂载 JSON CRUD 路由（按需扩展 Huma）。
func (s *Service) Routes() []server.Route {
	return nil
}

func (s *Service) List(ctx context.Context, params crudx.ListParams) (List{{.TypeName}}Resp, error) {
	tx := s.client.Conn(ctx).Model(&{{.TypeName}}{}).Table("{{.PathSlug}}")
	tx, err := crudx.ApplyList(tx, s.listSpec, &params)
	if err != nil {
		return List{{.TypeName}}Resp{}, err
	}
	var total *int64
	if params.ReplyWithCount {
		var n int64
		if err := tx.Count(&n).Error; err != nil {
			return List{{.TypeName}}Resp{}, fxerrors.Wrap(err)
		}
		total = &n
	}
	var rows []{{.TypeName}}
	if err := tx.Find(&rows).Error; err != nil {
		return List{{.TypeName}}Resp{}, fxerrors.Wrap(err)
	}
	items := make([]{{.TypeName}}ListRow, 0, len(rows))
	for _, row := range rows {
		items = append(items, to{{.TypeName}}ListRow(row))
	}
	return List{{.TypeName}}Resp{
		Items: items,
		Total: total,
		Start: params.Start,
		Limit: params.Limit,
	}, nil
}

type list{{.TypeName}}Input struct {
	fxhuma.ListQueryInput
}

type list{{.TypeName}}Output struct {
	Body List{{.TypeName}}Resp
}

// NewHumaRegistrar registers GET {{.Path}} (ZStack-style q= list).
func NewHumaRegistrar(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.Register(api, huma.Operation{
			OperationID: "list{{.TypeName}}",
			Method:      http.MethodGet,
			Path:        "{{.Path}}",
			Summary:     "List {{.Tag}}",
			Tags:        []string{"{{.Tag}}"},
		}, func(ctx context.Context, in *list{{.TypeName}}Input) (*list{{.TypeName}}Output, error) {
			params, err := fxhuma.ListParamsFromInput(&in.ListQueryInput)
			if err != nil {
				return nil, err
			}
			out, err := svc.List(ctx, params)
			if err != nil {
				return nil, err
			}
			return &list{{.TypeName}}Output{Body: out}, nil
		})
	})
}

func (s *Service) Get(ctx context.Context, req Get{{.TypeName}}Req) ({{.TypeName}}Detail, error) {
	row, err := crudx.FirstByID[{{.TypeName}}](ctx, s.client.Conn(ctx), req.ID)
	if err != nil {
		return {{.TypeName}}Detail{}, err
	}
	return to{{.TypeName}}Detail(*row), nil
}

func (s *Service) Create(ctx context.Context, req Create{{.TypeName}}Req) ({{.TypeName}}Detail, error) {
	u := {{.TypeName}}{}
{{- range .Fields}}
	u.{{.Name}} = req.{{.Name}}
{{- end}}
	crudx.ZeroPrimaryKey[{{.TypeName}}](&u)
	if err := s.client.Conn(ctx).Create(&u).Error; err != nil {
		return {{.TypeName}}Detail{}, crudx.MapDBError(err)
	}
	out, err := crudx.FirstByID[{{.TypeName}}](ctx, s.client.Conn(ctx), strconv.FormatInt(u.ID, 10))
	if err != nil {
		return {{.TypeName}}Detail{}, err
	}
	return to{{.TypeName}}Detail(*out), nil
}

func (s *Service) Update(ctx context.Context, req Update{{.TypeName}}Req) ({{.TypeName}}Detail, error) {
	existing, err := crudx.FirstByID[{{.TypeName}}](ctx, s.client.Conn(ctx), req.ID)
	if err != nil {
		return {{.TypeName}}Detail{}, err
	}
	next := *existing
{{- range .Fields}}
	next.{{.Name}} = req.{{.Name}}
{{- end}}
	if err := s.client.Conn(ctx).Save(&next).Error; err != nil {
		return {{.TypeName}}Detail{}, crudx.MapDBError(err)
	}
	out, err := crudx.FirstByID[{{.TypeName}}](ctx, s.client.Conn(ctx), req.ID)
	if err != nil {
		return {{.TypeName}}Detail{}, err
	}
	return to{{.TypeName}}Detail(*out), nil
}

func (s *Service) Delete(ctx context.Context, req Delete{{.TypeName}}Req) (delete{{.TypeName}}Resp, error) {
	row, err := crudx.FirstByID[{{.TypeName}}](ctx, s.client.Conn(ctx), req.ID)
	if err != nil {
		return delete{{.TypeName}}Resp{}, err
	}
	if err := s.client.Conn(ctx).Delete(row).Error; err != nil {
		return delete{{.TypeName}}Resp{}, crudx.MapDBError(err)
	}
	return delete{{.TypeName}}Resp{}, nil
}

func to{{.TypeName}}ListRow(m {{.TypeName}}) {{.TypeName}}ListRow {
	return {{.TypeName}}ListRow{
		ID: m.ID,
{{- range .Fields}}
		{{.Name}}: m.{{.Name}},
{{- end}}
	}
}

func to{{.TypeName}}Detail(m {{.TypeName}}) {{.TypeName}}Detail {
	return {{.TypeName}}Detail{
		ID: m.ID,
{{- range .Fields}}
		{{.Name}}: m.{{.Name}},
{{- end}}
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// Module 装配 {{.TypeName}} Huma list API。
var Module = fx.Module("{{.Package}}",
	fx.Provide(NewService),
	server.ProvideRoutes((*Service).Routes),
	fxhuma.ProvideRegistrar(NewHumaRegistrar),
)
`
