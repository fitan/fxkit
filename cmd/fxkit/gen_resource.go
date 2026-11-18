package main

import (
	"bytes"
	"fmt"
	"go/format"
	"text/template"
)

func renderResource(data *resourceTemplate) ([]byte, error) {
	tmpl, err := template.New("resource").Funcs(funcs).Parse(resourceTmpl)
	if err != nil {
		return nil, fmt.Errorf("template parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("template execute: %w", err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt generated source: %w (this is a fxkit bug; please report it)", err)
	}
	return formatted, nil
}

// resourceTmpl 是 `fxkit gen resource` 输出的 Go 源码模板。
//
// 所有插入 Go 字符串字面量的值都经 {{quote .X}}（strconv.Quote）转义，
// 避免 --table / --sort 等输入拼出非法或恶意源码。
// Struct tag 位于反引号内，其内容只由已校验的字段名与固定标记片段组成。
const resourceTmpl = `// 由 fxkit gen resource 生成；提交后可自由编辑。
package {{.Package}}

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/crudx"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/di"
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

// TableName 把表名固定下来：crudx.List 的列限定与 GORM 的默认推导必须一致，
// 否则 --table 会只影响 List，而 Create/Get/AutoMigrate 仍指向推导出的表名。
func ({{.TypeName}}) TableName() string { return {{quote .Table}} }

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

type Update{{.TypeName}}Req struct {
	ID string ` + "`json:\"id\"`" + `
{{- range .Fields}}
	{{.Name}} {{if .Optional}}*{{end}}{{.GoType}} ` + "`json:\"{{.JSONName}}{{if .Optional}},omitempty{{end}}\"`" + `
{{- end}}
}

// Service 显式实现 List/Get/Create/Update/Delete。List 走 crudx.List。
type Service struct {
	client   *gormx.Client
	listSpec crudx.ListSpec
}

func NewService(client *gormx.Client, lc *di.Lifecycle) (*Service, error) {
	if client == nil || !client.Enabled() {
		return nil, fxerrors.Internal({{quote .NilClientMsg}})
	}
	if _, err := crudx.ResolveModel[{{.TypeName}}](client.Conn(context.Background())); err != nil {
		return nil, err
	}
	s := &Service{
		client: client,
		listSpec: crudx.ListSpec{
			Fields: map[string]crudx.FieldSpec{
{{- range .ListFields}}
				{{quote .Name}}: {Column: {{quote .Column}}, Kind: {{.Kind}}{{if .Indexed}}, Indexed: true{{end}}{{if .Like}}, Likeable: true{{end}}},
{{- end}}
			},
			SortFields: map[string]string{
{{- range .SortFields}}
				{{quote .Name}}: {{quote .Column}},
{{- end}}
			},
			DefaultSort: {{quote .SortBy}},
			Limits:      crudx.Limits{LimitDefault: 20, LimitMax: 100},
		},
	}
	lc.Append(di.Hook{
		OnStart: func(ctx context.Context) error {
			var z {{.TypeName}}
			return client.Conn(ctx).AutoMigrate(&z)
		},
	})
	return s, nil
}

func (s *Service) List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[{{.TypeName}}ListRow], error) {
	return crudx.List(ctx, crudx.ListInput[{{.TypeName}}, {{.TypeName}}ListRow]{
		DB:     s.client.Conn(ctx).Model(&{{.TypeName}}{}).Table({{quote .Table}}),
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
		// 只写入请求里真正提供的字段：optional 字段用指针区分「未提供」与「显式零值」，
		// 避免 PATCH 式请求把未传字段变成零值。
		updates := map[string]any{}
{{- range .Fields}}
{{- if .Optional}}
		if req.{{.Name}} != nil {
			next.{{.Name}} = *req.{{.Name}}
			updates[{{quote .Column}}] = *req.{{.Name}}
		}
{{- else}}
		next.{{.Name}} = req.{{.Name}}
		updates[{{quote .Column}}] = req.{{.Name}}
{{- end}}
{{- end}}
		if len(updates) > 0 {
			if err := s.client.Conn(txCtx).Model(&next).Updates(updates).Error; err != nil {
				return {{.TypeName}}Detail{}, crudx.MapDBError(err)
			}
		}
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
			Path: {{quote .Path}},
			Tags: []string{ {{quote .Tag}} },
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

var Module = di.Options(
	fxkit.Service({{quote .Package}},
		fxkit.Provide(func(i di.Injector) (*Service, error) {
			return NewService(di.MustInvoke[*gormx.Client](i), di.MustInvoke[*di.Lifecycle](i))
		}),
	),
	fxhuma.ProvideRegistrarLazy(func(i di.Injector) (fxhuma.Registrar, error) {
		return NewHumaRegistrar(di.MustInvoke[*Service](i)), nil
	}),
)
`
