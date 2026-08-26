---
name: fxkit-huma-crud
description: >-
  Implement Huma v2 REST on fxkit with crudx.List (ZStack q=/cursor, ListSpec
  whitelist), ListQueryInput, and optional RegisterResource. Use when the user
  asks for REST list APIs, q= filters, cursor pagination, fxkit gen resource,
  or JSON get/create/update/delete on an fxkit service. Pair with fxkit for
  module wiring and fxkit-authz for permissions. Do not reach for NewRepo
  unless the user already uses it.
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
	DB:   client.Conn(ctx).Model(&User{}),
	Spec: userListSpec,
	Params: crudx.ListParams{
		Limit:     20,
		UseCursor: true,
		SortBy:    "id",
		Q:         []string{"name~alice", "or(status=active,status=pending)"},
	},
	ToRow: func(u User) UserRow { return UserRow{ID: u.ID, Name: u.Name} },
})
```

能力：受控 `q=`（`or(...)`、`.isnull` / `.notnull`；比较运算不能配 null）、keyset cursor（**不能**按关联字段排序）、JOIN 时根表 `DISTINCT` + Count Distinct(PK)、LIKE 转义 `%`/`_`。软删：`gorm.DeletedAt`。

HTTP 入参嵌入 `fxhuma.ListQueryInput`，再 `ListParamsFromInput`。Huma 级中间件：`fxhuma.ProvideMiddleware(...)`（与 `server.ProvideMiddleware` 不同层）。

### 单个 list operation

```go
fxhuma.ProvideRegistrar(func(svc *Service) fxhuma.Registrar {
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
	})
})
```

读一条：`crudx.GetByID` / `FirstByID`。写：`ZeroPrimaryKey`、`Select` 列更新、`MapDBError`。不要对 `ApplyList` 的返回值做 `Count`（Limit 已加上，total 会被截断）；需要 count / `nextCursor` 时用 `crudx.List`。

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
fxhuma.ProvideRegistrar(func(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.RegisterResource(api, svc, fxhuma.RegisterResourceInput[UpdateReq]{
			Path: "/articles",
			Tags: []string{"Articles"},
			BindUpdate: func(id string, body UpdateReq) UpdateReq {
				body.ID = id
				return body
			},
		})
	})
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

## 可选：Repo[T]

兼容薄封装（Create/GetByID/List/Update/Delete）。新代码不必嵌入 `*crudx.Repo[T]`；领域查询直接 `Conn(ctx).Where(...)`。已有 Repo 代码可继续用。

## 错误

- 返回 `*fxerrors.Error`（`NotFound` / `Validation` / `Conflict` / …）。
- DB 错误经 `crudx.MapDBError` 再抛。
- 其他 error → Huma 映射为 **HTTP 500 + `"internal error"`**。

## 鉴权挂钩

受保护路由：`auto_register_routes` 会刮取 OpenAPI（含 `RegisterResource`）；也可 `authz.ProvideHTTPRoutes` 或 `fxhuma.ResourceOperations` + `HTTPRoutesFromOperations`。细节见 `fxkit-authz`。裸 chi handler **不经** authz。

## Agent 检查清单

- [ ] `huma.Module` 已进 `fxkit.Run`。
- [ ] Registrar 经 `fxhuma.ProvideRegistrar`。
- [ ] 列表走 `crudx.List` + `ListSpec` 白名单，不要自写 q= parser。
- [ ] LIKE 字段 `Indexed: true`；`fxkit gen resource --search` 会给非 TEXT 列加 GORM index。
- [ ] 不要对 `ApplyList` 结果 `Count`；JOIN 列表会 Distinct。
- [ ] 写操作显式 `Select` 列，避免 `Save` 全列更新。
- [ ] 需要五条 REST 时才用 `RegisterResource`；Update 路径 id 经 `BindUpdate` 写入 body。
- [ ] 需要权限时同步登记 `authz.HTTPRoute`，或依赖 OpenAPI 刮取。
