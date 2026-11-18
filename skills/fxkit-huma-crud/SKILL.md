---
name: fxkit-huma-crud
description: >-
  Implement Huma v2 REST on fxkit with crudx.List (ZStack q=/cursor, ListSpec
  whitelist), ListQueryInput, and optional RegisterResource. Use when the user
  asks for REST list APIs, q= filters, cursor pagination, fxkit gen resource,
  or JSON get/create/update/delete on an fxkit service. Pair with fxkit for
  module wiring and fxkit-authz for permissions. Do not wrap CRUD in a generic
  Repo[T]; if the project needs a shared repository layer, follow fxkit-layout
  and keep writes orchestrated in the service.
---

# fxkit Huma + crudx

`crudx` 是**受控 list 查询引擎**（统一 `q=` / cursor / 字段白名单），不是泛型 CRUD 框架。
Create/Update/Delete 在 Service 里显式写。`huma.Module` **不在** `fxkit.Default()` 内：

```go
fxkit.Run(fxkit.Default(), huma.Module, users.Module)
```

## 列表引擎（必用）

`ListSpec` 是过滤/排序白名单——这是给 AI 和前端的契约，不要每张表重写 parser。

```go
page, err := crudx.List(ctx, crudx.ListInput[User, UserRow]{
	DB:       client.Conn(ctx).Model(&User{}),
	Spec:     userListSpec,
	Params:   params,
	Preloads: []string{"Profile"}, // 声明式预加载，避免 N+1
	ToRow: func(u User) UserRow {
		return UserRow{ID: u.ID, Name: u.Name, Status: u.Status, CreatedAt: u.CreatedAt}
	},
})
```

### 查询能力与安全护栏
- **受控 `q=`**：
  - 支持操作符：`=`、`!=`、`~`（LIKE，需 `Indexed: true`）、`!~`、`>`、`>=`、`<`、`<=`、`.in:`、`.notin:`、`.isnull`、`.notnull`；
  - 表达式按最早出现的操作符精准切分，字段值内包含特殊字符或等号不影响切分；
  - 支持单/双引号字面量保护，如 `q=name="Doe, John"` 或 `q=tag='a,b'`（避免逗号误切）；
  - 支持受控 OR：`q=or(status=active,status=pending)`；
  - 空枚举防护：`.in:` 为空数组时自动翻译为 `1 = 0`，防止 SQL 语法报错；
- **键集游标分页（Keyset Cursor）**：
  - 基于排序值与主键组合编码为安全 token（`NextCursor`）；
  - 递归支持内嵌结构体排序（如 `gorm.Model` 中的 `CreatedAt`、`ID`）；
  - 自动识别并解析 RFC3339 时间戳对象，天然适配各类数据库驱动 timestamp 比较；
  - **不能**按跨表关联字段（Relation）进行游标分页（会报 400）；
- **关联查询**：
  - 支持多表按需 `LEFT JOIN`；根表自动 `DISTINCT` + `Count Distinct(PK)`；
  - LIKE 关键字自动转义 `%`、`_` 与 `\`。
- **软删除**：自动适配 `gorm.DeletedAt`。

HTTP 入参嵌入 `fxhuma.ListQueryInput`，再 `ListParamsFromInput`。Huma 级中间件：`fxhuma.ProvideMiddleware(...)`（与 `server.ProvideMiddleware` 不同层）。

### 分页结果投影
- **`ListResult[T].Map[R](fn)`**：直接在分页结果信封上进行类型投影，保留 `Total`、`Start`、`Limit`、`NextCursor` 分页元数据。
- 推荐分层：**repo 返回 `ListResult[entity.X]`**（`ToRow` 用显式 identity：`func(m entity.X) entity.X { return m }`，零反射、编译期校验），**service 用 `Map` 转成 `ListResult[XxxRow]`**。这样 repo 不绑定 HTTP 契约。
- 只有当读模型需要 entity 装不下的字段（JOIN 输出/聚合）时，才在 repository/query 层投影。

### 单个 list operation 示例

```go
fxhuma.ProvideRegistrarLazy(func(i di.Injector) (fxhuma.Registrar, error) {
	svc := di.MustInvoke[*Service](i)
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.Register(api, huma.Operation{
			OperationID: "listUsers",
			Method:      http.MethodGet,
			Path:        "/users",
			Tags:        []string{"Users"},
		}, func(ctx context.Context, in *ListUsersInput) (*ListUsersOutput, error) {
			params, err := fxhuma.ListParamsFromInput(&in.ListQueryInput)
			if err != nil {
				return nil, err
			}
			out, err := svc.List(ctx, params)
			if err != nil {
				return nil, err
			}
			return &ListUsersOutput{Body: out}, nil
		})
	}), nil
})
```

读一条：`crudx.GetByID` / `FirstByID`。写：`ZeroPrimaryKey`、`Select` 列更新、`MapDBError`。不要对 `ApplyList` 的返回值做 `Count`（Limit 已加上，total 会被截断）；需要 count / `nextCursor` 时用 `crudx.List`。

并发更新：模型实现 `GetVersion/SetVersion`，用 `gormx.UpdateWithVersion(ctx, db, &m, "title", "body")`；冲突返回 409。

## 可选：RegisterResource

只在需要一次挂五条 REST 时用。服务实现：

```go
List(ctx, crudx.ListParams) (crudx.ListResult[ListRow], error)
Get(ctx, id string) (Detail, error)
Create(ctx, req CreateReq) (Detail, error)
Update(ctx, req UpdateReq) (Detail, error)
Delete(ctx, id string) error
```

```go
fxhuma.ProvideRegistrarLazy(func(i di.Injector) (fxhuma.Registrar, error) {
	svc := di.MustInvoke[*Service](i)
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.RegisterResource(api, svc, fxhuma.RegisterResourceInput[UpdateReq]{
			Path: "/articles",
			Tags: []string{"Articles"},
			BindUpdate: func(id string, body UpdateReq) UpdateReq {
				body.ID = id
				return body
			},
		})
	}), nil
})
```

| 方法 | 路径 |
|------|------|
| GET | `{path}` 列表 |
| GET | `{path}/{id}` |
| POST | `{path}` |
| PUT | `{path}/{id}`（需 `BindUpdate`） |
| DELETE | `{path}/{id}` |

无 `BindUpdate` → 不挂 PUT。只做列表时用上一节的单个 operation，不要硬套五件套。

脚手架：`fxkit gen resource` 生成 `crudx.List` + 显式写方法 + `RegisterResource`。

给其它服务生成 typed 客户端：先 `<svc> openapi` dump 3.0 spec，再 `fxkit gen client`（见 `fxkit-docs` / `fxkit-reqx`）。不要在本服务再生存一份自己的 Go SDK。

## 仓储层

`crudx` 不提供泛型 `Repo[T]`。需要进程内共享仓储时，按 `fxkit-layout` 的 `internal/repository` 组织：一聚合一文件，仓储只依赖 `entity`，写操作和事务边界仍留在 service。

## 错误

- 返回 `*fxerrors.Error`（`NotFound` / `Validation` / `Conflict` / …）。
- DB 错误经 `crudx.MapDBError` 再抛。
- 其他 error → Huma 映射为 **HTTP 500 + `"internal error"`**。

## 鉴权挂钩

受保护路由：`auto_register_routes` 会刮取 OpenAPI（含 `RegisterResource`）；也可 `authz.ProvideHTTPRoutes` 或 `fxhuma.ResourceOperations` + `HTTPRoutesFromOperations`。细节见 `fxkit-authz`。裸 chi 同样经过 authz，匿名路径写 `auth.public_paths`。

## Agent 检查清单

- [ ] `huma.Module` 已进 `fxkit.Run`。
- [ ] Registrar 经 `fxhuma.ProvideRegistrar`。
- [ ] 列表走 `crudx.List` + `ListSpec` 白名单，不要自写 q= parser。
- [ ] 预加载使用 `Preloads: []string{...}` 或 `Preload` 钩子，防范 N+1 查询。
- [ ] LIKE 字段 `Indexed: true`；`fxkit gen resource --search` 会给非 TEXT 列追加 GORM `index`，并标记 `Indexed: true` + `Likeable: true`。
- [ ] 不要对 `ApplyList` 结果 `Count`；JOIN 列表会自动 Distinct。
- [ ] 写操作显式 `Select` 列，避免 `Save` 全列更新。
- [ ] 不要使用已移除的 `crudx.Repo[T]`；需要仓储层时按 `fxkit-layout`。
- [ ] 需要五条 REST 时才用 `RegisterResource`；Update 路径 id 经 `BindUpdate` 写入 body。
- [ ] 需要权限时同步登记 `authz.HTTPRoute`，或依赖 OpenAPI 刮取。
