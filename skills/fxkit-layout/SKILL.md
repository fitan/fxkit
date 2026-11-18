---
name: fxkit-layout
description: >-
  Design, review, or refactor the directory and file layout of fxkit HTTP API services:
  monorepo services/<svc>, cmd/main.go wiring, internal/entity + internal/repository,
  feature packages (dto/service/handler/worker/module), naming conventions, dependency
  direction, multi-model endpoints, and cross-service boundaries. Use when the user asks
  how to lay out directories/files, split a service, place models/repositories, or handle
  endpoints that touch multiple models.
---

# fxkit 服务目录与文件结构

目标：多个服务各自独立进程；每个进程内有多个模型、多个接口。结构要让「模型归属、数据操作、业务、HTTP」四层一眼可见，并且编译期不产生 import cycle。

## 先判断三个层次

| 层次 | 单位 | 边界 |
|---|---|---|
| 进程 / 部署单元 | `services/<svc>/cmd/main.go` | 一个 `main` = 一个进程 |
| 模块 / bounded context | `internal/<feature>` | 包边界 + 依赖方向 |
| 聚合 / 表 | `internal/entity` + `internal/repository` | 数据所有权 |

不要把这三个层次混用。`services/` 只是仓库约定，Go 真正强制的是 `internal` 规则。

## 仓库级布局

```text
repo/
├── go.mod
├── Makefile
├── docker-compose.yml
├── deploy/                     # traefik / postgres init / k8s
├── internal/                   # 跨服务共享：无状态、不拥有表
│   ├── stack/                  # fxkit.Default() 组合
│   └── testkit/
├── services/
│   ├── catalog/                # 进程 A
│   └── storefront/             # 进程 B
├── scripts/
└── web/
```

- `services/<svc>/internal/...` 只能被该服务 import；这是跨进程边界的编译期保障。
- 仓库根 `internal/` 可被所有服务 import，但只放 `stack`、`testkit` 这类无状态代码。
- **不要**把某个服务的 `entity` / `repository` / SQL 放到仓库根共享。

## 服务级布局

```text
services/<svc>/
├── cmd/main.go                 # 只 wiring
├── configs/
│   ├── config.yaml
│   └── config.demo.yaml
├── api/
│   ├── embed.go                # //go:embed openapi.base.yaml
│   ├── openapi.base.yaml       # 手写基座
│   └── openapi.yaml            # make openapi 产物
└── internal/
    ├── entity/                 # 进程内共享模型（叶子包）
    │   ├── article.go
    │   ├── comment.go
    │   └── migrate.go
    ├── repository/             # 汇总所有模型的持久化操作
    │   ├── module.go
    │   ├── article.go
    │   ├── comment.go
    │   ├── counter.go
    │   ├── payment.go          # 共享 repo：对账/报表也能复用，不必 import payments
    │   └── query.go            # 可选：跨聚合只读投影
    ├── article/                # feature：HTTP + 用例
    │   ├── dto.go
    │   ├── service.go
    │   ├── handler.go
    │   ├── worker.go
    │   └── module.go
    ├── comment/
    ├── payments/               # feature：状态机 + 账本 + webhook
    ├── demo/                   # 可选能力用例：按能力拆文件
    │   ├── dto.go
    │   ├── service.go
    │   ├── cache.go            # cachex.GetOrSet
    │   ├── config.go           # config.Dynamic
    │   ├── lock.go             # consulx.Locker
    │   ├── outbox.go           # DLQ list/requeue
    │   ├── tx.go               # gormx.AfterCommit
    │   ├── hatchet.go          # ScheduleOnce / RunNoWait / rate limit
    │   ├── worker.go           # durable task + DAG
    │   ├── handler.go
    │   └── module.go
    ├── system/                 # ops：ping/me/health 等
    └── cli/                    # 宿主 cobra 命令
```

`api/embed.go` 是必需的：`go:embed` 不能引用父目录，所以基座 YAML 必须和 embed 文件同目录。

> 与脚手架的关系：`fxkit new` 目前把 spec 生成到 `services/<svc>/openapi.yaml`，且不生成 `api/`。采用本结构时，同步修改 Makefile 的 `openapi -o`、`gen-client --spec`、`gen-web` 路径，并新增 `api/embed.go`；否则沿用脚手架默认的根 `openapi.yaml`。

## 依赖方向（硬约束）

```text
handler ──> service ──> repository ──> entity
   │            │
   └──> dto     └──> query (可选)
worker ──> service
module ── 只装配
```

- `entity` 只 import `time` / `gorm.io/gorm`。
- `repository` 只 import `entity` + `gormx` / `crudx` / `fxerrors`；**绝不 import 任何 feature 包**。需要框架能力时可以直接 import 框架包，但不要 import 本服务的 feature。
- `service` 不 import `huma` / `fxhuma`。
- `handler` 不直接访问 `gormx`。
- `module.go` 不含业务分支，只做 `Provide` / `Registrar` / `Routes`。

`repository` 不能 import feature 的原因：feature 要 import `repository`，反向即 import cycle。

默认约定：**repo 返回 entity，service 负责 entity → DTO / 读模型**。这样 repo 不绑定某个 HTTP 契约，可被 worker / CLI / 其它 feature 复用。

例外：当读取需要 entity 装不下的字段（JOIN 输出、聚合计数），或该读模型被多个 feature 复用时，才把读模型与查询放到 `repository/query.go`。不要为了「通用」把 API DTO 放进 repo。

## 文件命名

| 角色 | 文件 | 说明 |
|---|---|---|
| 模型 | `entity/<aggregate>.go` | GORM struct + `TableName()` |
| 迁移 | `entity/migrate.go` | 按聚合提供 `MigrateArticles` / `MigrateComments` |
| 传输对象 | `feature/dto.go` | `XxxReq` / `XxxDetail` / `XxxListRow` + `toXxx()` |
| 业务用例 | `feature/service.go` | 事务边界、outbox、authz 上下文、entity → DTO 投影 |
| 持久化 | `repository/<aggregate>.go` | 只做数据访问，返回 entity |
| 跨聚合只读 | `repository/query.go` | 仅当需要 JOIN 输出/聚合，或跨 feature 复用 |
| HTTP | `feature/handler.go` | `huma.Operation` + `Register` + `HTTPRoutes()` |
| 异步 | `feature/worker.go` | Hatchet task/actor、outbox 消费 |
| 装配 | `feature/module.go` | 唯一导出 `Module` |
| 生成客户端 | `clients/<owner>/client.gen.go` | 生成物，不手改 |
| 生成配置 | `clients/<owner>/oapi-codegen.yaml` | oapi-codegen 配置 |
| 契约 | `api/openapi.base.yaml` | 手写 |
| 契约产物 | `api/openapi.yaml` | `make openapi` |

规则：

- 包名小写、领域单数名词：`article`、`comment`、`billing`。
- 不用 `utils` / `common` / `helpers` / `base` / `types` / `platform` 当包名。
- 多资源同包时用 `资源_角色`：`article_handler.go`。
- 生成物用 `.gen.go` 后缀。
- 测试：`<role>_test.go`；跨 feature 测试用 `entity.MigrateXxx`，不要 import 对方 feature。

## 多模型接口怎么落

| 场景 | 放哪 | 依赖 |
|---|---|---|
| 单聚合读写 | `repository/<aggregate>.go`，返回 entity；service 转 DTO | `entity` |
| 跨模型只读，属于某端点 | repo 返回 entity，`feature/service.go` 用 `ListResult.Map` 投影 | `entity` |
| 跨模型只读，端点需要 JOIN 输出/聚合 | `repository/query.go` | `entity` |
| 跨模型只读，被多个 feature 复用 | `internal/repository/query.go` | `entity` |
| 跨聚合写入 | `feature/service.go` 编排，事务在 service | 多个 repo |
| 跨进程 | 生成客户端 / 事件 | OpenAPI 契约 |

`crudx` 只提供 `List` / `ApplyList` / `GetByID` 等函数，不提供泛型 `Repo[T]`；仓储层按本 skill 的 `internal/repository` 组织。

读模型示例：

```go
// internal/repository/query.go
type ArticleDetail struct {
	Article  entity.Article
	Comments []entity.Comment
	Counter  int64
}

func (q *Queries) ArticleDetail(ctx context.Context, id int64) (ArticleDetail, error) { ... }
```

事务示例：

```go
return s.db.WithTxResult(ctx, func(txCtx context.Context) (Detail, error) {
	if err := s.articles.Insert(txCtx, &a); err != nil {
		return Detail{}, err
	}
	if err := outbox.EnqueueTopicMsg(txCtx, ...); err != nil {
		return Detail{}, err
	}
	return s.query.ArticleDetail(txCtx, a.ID)
})
```

事务边界属于用例，不要藏进 repository；否则 outbox 无法和写入同事务。提交后副作用用 `gormx.AfterCommit(txCtx, fn)`：回滚时不执行，且回调拿到的是不带事务的 context。

### repo 返回 entity，service 投影

```go
// repository/comment.go：只返回 entity
func (r *CommentRepo) List(ctx context.Context, p crudx.ListParams) (crudx.ListResult[entity.Comment], error) {
    return crudx.List(ctx, crudx.ListInput[entity.Comment, entity.Comment]{
        DB: r.db.Conn(ctx).Model(&entity.Comment{}).Table("comments"),
        Spec: r.spec, Params: p,
        // 返回 entity 时用显式 identity；零反射、编译期校验。
        ToRow: func(c entity.Comment) entity.Comment { return c },
    })
}

// service/comment.go：投影为 API 读模型，保留分页元数据
res, err := s.comments.List(ctx, params)
if err != nil { return crudx.ListResult[CommentListRow]{}, err }
return res.Map(toCommentListRow), nil
```

`crudx.List` 的 `ToRow` 只是内存投影；SQL 仍然是 `Find(&[]entity.X)`。返回 entity 不会改变查询或引入 N+1，代价只是多一次切片拷贝。

## 共享 repo

多租户隔离模型已移除，框架默认单租户。需要隔离时请在数据层单独设计（RLS / 独立 schema / 独立实例），不要依赖 `tenant.Scope` 这类约定。

共享 repo 仍然推荐集中放在 `internal/repository/<aggregate>.go`，返回 entity，不绑定 HTTP 契约。

## 非 Huma 路由（pprof / MCP / 裸 chi）

Casbin 只自动注册 Huma operation + `authz.ProvideHTTPRoutes` 显式登记的路由。框架挂载的 `/debug/pprof/*`、`/mcp/*` 以及裸 chi 路由必须登记，否则 admin 也会 403：

```go
authz.ProvideHTTPRoutes(
    authz.HTTPRoute{Method: http.MethodGet, Path: "/debug/pprof/*", OperationID: "pprofProfile"},
    authz.HTTPRoute{Method: http.MethodGet, Path: "/mcp/*", OperationID: "mcpSSE"},
)
```

## 跨进程边界

- 需要别的服务的数据：调用它的 API（`internal/clients/<owner>` 生成客户端）或订阅事件。
- 不要共享 `entity`、`repository`、SQL、GORM model。
- 两个进程都要写同一批表 = 边界划错了，合并服务或重新划分。
- 只共享无状态代码（`stack`、`testkit`），放仓库根 `internal/` 或独立 module。

## DI 装配

```go
// internal/repository/module.go
var Module = di.Options(
	fxkit.Service("repository",
		fxkit.Provide(func(i di.Injector) (*ArticleRepo, error) {
			return NewArticleRepo(di.MustInvoke[*gormx.Client](i)), nil
		}),
	),
)
```

```go
// internal/article/module.go
var Module = di.Options(
	fxkit.Service("article",
		fxkit.Provide(func(i di.Injector) (*Service, error) {
			return NewService(
				di.MustInvoke[*gormx.Client](i),
				di.MustInvoke[*repository.ArticleRepo](i),
				di.MustInvoke[*di.Lifecycle](i),
			)
		}),
	),
	fxhuma.ProvideRegistrarLazy(...),
	hatchetx.ProvideRegistrarLazy(...),
	authz.ProvideHTTPRoutes(HTTPRoutes()...),
)
```

`cmd/main.go` 依次加入 `repository.Module` 和 feature modules；每个 `cmd` 只做一个进程。

## 迁移与测试

```go
// entity/migrate.go
func MigrateArticles(db *gorm.DB) error { return db.AutoMigrate(&Article{}, &ArticleFanout{}) }
func MigrateComments(db *gorm.DB) error { return db.AutoMigrate(&Comment{}) }
```

- 每个 feature 的 `NewService` 在 `di.Lifecycle.OnStart` 里只迁移自己拥有的表。
- 测试需要别的表时调用对应 `entity.MigrateXxx`，不要 import 对方 feature。
- repository 测试用 SQLite；service 测试可直接用真实 repo，需要 fake 时再在消费方定义接口。

## HTTP 命名

- Path：复数名词 + kebab-case，`/articles`、`/articles/{id}`、`/articles/{id}/comments`。
- OperationID：动词 + 资源复数，camelCase：`listArticles`、`createArticle`。
- Tag：资源复数，Title Case：`Articles`。
- JSON：camelCase；Query：项目内统一一种，并在 `doc.go` 或 README 说明。
- 错误统一 `fxerrors`，不手写错误 JSON。
- 对外 API 可加 `/v1`；内部服务可不加。

## 反模式

- `internal/models` / `internal/repository` 里混放 DTO、业务方法。
- repository 返回 `huma` 类型或 HTTP 错误体。
- repository 定义只被单个 HTTP 接口使用的 DTO；应返回 entity，由 service 投影（除非该读模型需要 JOIN 输出/聚合）。
- service 把 `*http.Request` 传进 repository。
- 为每个 repo 方法写 interface，但没有 fake 或第二实现。
- 把 `AutoMigrate` 分散在 service 里，模型却在 `entity`。
- 跨进程共享 `entity` / GORM model / SQL。
- 所有 SQL 堆在一个 `repository.go` 文件；应按聚合拆文件。

## 重构顺序

1. 抽 `entity` + `migrate.go`，测试改 `entity.MigrateXxx`。
2. 建 `repository/`，按聚合拆 `article.go` / `comment.go`；把 `crudx.ListSpec` 移进去。
3. 拆 feature 的 `dto.go` / `service.go` / `handler.go` / `worker.go` / `module.go`。
4. ops/CLI 归位：`system/`、`cli/`。
5. 契约归位：`api/embed.go` + Makefile 路径。
6. 只在出现真正跨聚合只读端点时加 `query.go`。

参考实现：`example/services/catalog/internal/{entity,repository,article,comment,payments,demo,system}`。

## 小服务例外

单资源、< ~150 行时允许 `feature/<name>.go` 单文件。超过 ~200 行或出现 3 种以上职责再按角色拆。不要为了拆而拆。
