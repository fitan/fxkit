package main

import (
	"bytes"
	"fmt"
	"go/format"
	"text/template"
)

func renderResource(data *resourceTemplate) ([]byte, error) {
	tmpl, err := template.New("resource").Parse(resourceTmpl)
	if err != nil {
		return nil, fmt.Errorf("template parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("template execute: %w", err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return buf.Bytes(), fmt.Errorf("gofmt generated source: %w", err)
	}
	return formatted, nil
}

// resourceTmpl 是 `fxkit gen resource` 输出的 Go 源码模板。
const resourceTmpl = `// 由 fxkit gen resource 生成；提交后可自由编辑。
package {{.Package}}

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	fxhuma "github.com/fitan/fxkit/huma"
	"go.uber.org/fx"
)

// {{.TypeName}} 是 GORM model。JSON tag 为 "-" 表示不直接暴露；请调整 List/Detail DTO。
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

// Service 显式实现 List/Get/Create/Update/Delete。List 走 crudx.List。
type Service struct {
	client   *gormx.Client
	listSpec crudx.ListSpec
}

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
{{- range .ListFields}}
				"{{.Name}}": {Column: "{{$.PathSlug}}.{{.Name}}", Kind: {{.Kind}}{{if .Indexed}}, Indexed: true{{end}}},
{{- end}}
			},
			SortFields: map[string]string{
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

func (s *Service) List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[{{.TypeName}}ListRow], error) {
	return crudx.List(ctx, crudx.ListInput[{{.TypeName}}, {{.TypeName}}ListRow]{
		DB:     s.client.Conn(ctx).Model(&{{.TypeName}}{}).Table("{{.PathSlug}}"),
		Spec:   s.listSpec,
		Params: params,
		ToRow:  to{{.TypeName}}ListRow,
	})
}

func (s *Service) Get(ctx context.Context, id string) ({{.TypeName}}Detail, error) {
	return crudx.GetByID[{{.TypeName}}, {{.TypeName}}Detail](ctx, s.client.Conn(ctx), id, to{{.TypeName}}Detail)
}

func (s *Service) Create(ctx context.Context, req Create{{.TypeName}}Req) ({{.TypeName}}Detail, error) {
	u := {{.TypeName}}{
{{- range .Fields}}
		{{.Name}}: req.{{.Name}},
{{- end}}
	}
	crudx.ZeroPrimaryKey[{{.TypeName}}](&u)
	if err := s.client.Conn(ctx).Create(&u).Error; err != nil {
		return {{.TypeName}}Detail{}, crudx.MapDBError(err)
	}
	return to{{.TypeName}}Detail(u), nil
}

func (s *Service) Update(ctx context.Context, req Update{{.TypeName}}Req) ({{.TypeName}}Detail, error) {
	return s.client.WithTxResult(ctx, func(txCtx context.Context) ({{.TypeName}}Detail, error) {
		existing, err := crudx.FirstByID[{{.TypeName}}](txCtx, s.client.Conn(txCtx), req.ID)
		if err != nil {
			return {{.TypeName}}Detail{}, err
		}
		next := *existing
{{- range .Fields}}
		next.{{.Name}} = req.{{.Name}}
{{- end}}
{{- if .Fields}}
		if err := s.client.Conn(txCtx).Select({{range $i, $f := .Fields}}{{if $i}}, {{end}}"{{$f.Column}}"{{end}}).Updates(&next).Error; err != nil {
			return {{.TypeName}}Detail{}, crudx.MapDBError(err)
		}
{{- end}}
		out, err := crudx.FirstByID[{{.TypeName}}](txCtx, s.client.Conn(txCtx), req.ID)
		if err != nil {
			return {{.TypeName}}Detail{}, err
		}
		return to{{.TypeName}}Detail(*out), nil
	})
}

func (s *Service) Delete(ctx context.Context, id string) error {
	row, err := crudx.FirstByID[{{.TypeName}}](ctx, s.client.Conn(ctx), id)
	if err != nil {
		return err
	}
	if err := s.client.Conn(ctx).Delete(row).Error; err != nil {
		return crudx.MapDBError(err)
	}
	return nil
}

func NewHumaRegistrar(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.RegisterResource(api, svc, fxhuma.RegisterResourceInput[Update{{.TypeName}}Req]{
			Path: "{{.Path}}",
			Tags: []string{"{{.Tag}}"},
			BindUpdate: func(id string, body Update{{.TypeName}}Req) Update{{.TypeName}}Req {
				body.ID = id
				return body
			},
		})
	})
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

var Module = fx.Module("{{.Package}}",
	fx.Provide(NewService),
	fxhuma.ProvideRegistrar(NewHumaRegistrar),
)
`
