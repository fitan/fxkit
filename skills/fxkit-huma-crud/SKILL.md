---
name: fxkit-huma-crud
description: >-
  Implement Huma v2 REST APIs on fxkit with RegisterResource, ListQueryInput,
  crudx.List, and crudx.NewRepo generic repositories. Use when the user asks for
  REST CRUD, Huma operations, ZStack-style q= filters, cursor pagination,
  embedding Repo[T], fxkit gen resource, or JSON list/get/create/update/delete
  endpoints on an fxkit service — even if they don't say "RegisterResource".
  Pair with fxkit for module wiring and fxkit-authz for permissions.
---

# fxkit Huma + crudx

`huma.Module` **不在** `fxkit.Default()` 内，REST 必须显式加入：

```go
fxkit.Run(fxkit.Default(), huma.Module, users.Module)
```

## 标准 CRUD：RegisterResource

服务实现：

```go
List(ctx, crudx.ListParams) (crudx.ListResult[ListRow], error)
Get(ctx, id string) (Detail, error)
Create(ctx, req CreateReq) (Detail, error)
Update(ctx, req UpdateReq) (Detail, error)
Delete(ctx, id string) error
```

注册：

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

无 `BindUpdate` → 不挂 PUT。

## 单个 operation

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

列表入参嵌入 `fxhuma.ListQueryInput`（limit、cursor、`q=` 等）。

Huma 级中间件：`fxhuma.ProvideMiddleware(...)`（与 `server.ProvideMiddleware` 不同层）。

## crudx 泛型仓库（推荐数据层）

业务仓库**嵌入** `*crudx.Repo[T]`，只加领域查询：

```go
type UserRepo struct {
	*crudx.Repo[User]
}

func NewUserRepo(client *gormx.Client) (*UserRepo, error) {
	base, err := crudx.NewRepo[User](crudx.RepoConfig{
		Client:        client,
		Spec:          userListSpec,       // 过滤/排序白名单
		UpdateColumns: []string{"name", "email"}, // Update 默认列
	})
	if err != nil {
		return nil, err
	}
	return &UserRepo{Repo: base}, nil
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	return r.First(ctx, crudx.WhereInput{Query: "email = ?", Args: []any{email}, Detail: "email=" + email})
}
```

`Repo` 已有 Create/GetByID/List/Update/Delete + First/Exists；事务用 `Repo.Transaction` 或 `gormx.Client.Transaction`。多表 + outbox 同事务见 `fxkit-hatchet`。

## crudx 列表引擎

在 Service 内也可直接：

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

能力：受控 `q=`（含 `or(...)`、`.isnull` / `.notnull`）、cursor、JOIN Count Distinct(PK)、`GetByID` / `MapDBError`、`Preload`。软删：`gorm.DeletedAt`。

脚手架：`fxkit gen resource` 生成同款形态。

## 错误

- 返回 `*fxerrors.Error`（`NotFound` / `Validation` / `Conflict` / …）。
- DB 错误可经 `crudx.MapDBError` 再抛。
- 其他 error → Huma 映射为 **HTTP 500 + `"internal error"`**。

## 鉴权挂钩

受保护路由用 `authz.ProvideHTTPRoutes`（或从 Huma Operation 生成）登记 path/method；细节见 `fxkit-authz`。裸 chi handler **不经** authz。

## Agent 检查清单

- [ ] `huma.Module` 已进 `fxkit.Run`。
- [ ] Registrar 经 `fxhuma.ProvideRegistrar`，不要只在 init 里偷偷注册。
- [ ] List/Repo Spec 过滤字段白名单，避免任意列注入。
- [ ] `UpdateColumns` 或 `UpdateInput.Columns` 已设，避免全列更新。
- [ ] Update 路径 id 经 `BindUpdate` 写入 body。
- [ ] 需要权限时同步登记 `authz.HTTPRoute`。
