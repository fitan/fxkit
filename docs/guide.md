# fxkit 整体使用指南

> 这是一份**按使用旅程组织**的指南：从最小服务一路加到数据库、列表、鉴权、异步、可观测性、上线。
> 需要查具体配置项/API 时，配合 [`README.md`](../README.md) 的分章参考使用。

---

## 0. 这份文档怎么读

| 你的情况 | 建议路径 |
|---|---|
| 第一次接触 fxkit | §1 定位 → §2 心智模型 → §3 Step 1–2 |
| 要接数据库 / 写列表接口 | §3 Step 3–4 + `crudx/README.md` |
| 要做鉴权 | §3 Step 5 + README §7 |
| 要做异步 / 定时 / 工作流 | §3 Step 6 + README §11–12 |
| 要上线 | §7 + `docs/production-checklist.md` |
| 要写测试 | §6 + `docs/testing.md` |
| 出问题了 | §8 故障排查 |

---

## 1. 它是什么 / 不是什么

**是什么**

- 一个**有主见的内部平台框架**：约定好配置、HTTP、REST、鉴权、数据库、列表查询、事务发件箱、工作流、服务发现、可观测性、文档与脚手架。
- 适合**多个结构相似的服务**共用一套工程规范。

**不是什么**

- 不是「万能 ORM / 泛型 CRUD」：`crudx` 只做受控列表，写操作要显式实现。
- 不是「无约定」：DI 装配有严格阶段，违反会 panic（严格模式）。
- 不是开箱即公网安全：`docs`/`mcp` 默认关闭、限流要显式开、`dev_header_user` 必须生产关闭。

---

## 2. 心智模型（先看这四件事）

### 2.1 Bundle：三个入口

```go
fxkit.Core()      // config + buildinfo + otelx + server(+listener) + cli
fxkit.Default()   // Core + docs/gormx/hatchetx/outbox/authz/consulx/reqx/mcpx/cachex
fxkit.Minimal()   // Core 的别名（历史名）
```

- **只写 HTTP 服务** → `Core()`。
- **要数据库/鉴权/工作流** → `Default()`，再按需加 `huma.Module`。
- `docs` 与 `mcp` 即使被打包也**默认不暴露**，需 `docs.enabled` / `mcp.enabled`。

### 2.2 四阶段生命周期（最重要的约束）

| 阶段 | 优先级 | 用途 |
|---|---|---|
| `PhaseResolveContributors` | 0 | 求值 `func(*Svc) T` 这类延迟构造函数，写入 group |
| `PhaseWireGroup` | 100 | 固化 group（Huma 安装 middleware + registrar） |
| `PhasePostWire` | 150 | 依赖已安装结果（`/docs` 合并 live spec、MCP 提取 tools） |
| `PhaseMountHTTP` | 200 | 把 `server.Route` 挂到 chi mux、注册健康检查 |

**铁律：不要在模块的 `Option` 闭包里读 `di.GetGroup`。** 那时业务模块还没装配完，只能读到一部分。
正确做法是在 `Lifecycle.PreStart` 里读。

开严格模式把这类错误变成 panic：

```bash
FXKIT_STRICT_WIRING=1 go test ./...    # 或 di.SetStrictWiring(true)
```

### 2.3 DI 与 group

```go
fxkit.Service("orders",
    fxkit.Provide(func(i di.Injector) (*Service, error) {   // 主构造函数
        return NewService(di.MustInvoke[*OrdersConfig](i))
    }),
    fxkit.ProvideValue(&Repo{}),                            // 额外服务
    fxkit.Routes(newRoutes()...),                          // 贡献 []server.Route
    fxkit.Invoke(func(i di.Injector) error {               // OnStart 副作用
        return di.MustInvoke[*Service](i).Seed()
    }),
    fxkit.ProvideConfig[OrdersConfig]("orders"),
)
```

构造函数统一签名 **`func(di.Injector) (T, error)`**，依赖用 `di.MustInvoke` 显式取。
早期版本靠反射解析 `func(dep...) T` / 参数结构体，现在全部取消：接线错误在**编译期**暴露。

### 2.4 配置模型

```go
type OrdersConfig struct {
    PageSize int `yaml:"page_size"`
}

func (c *OrdersConfig) SetDefaults() { c.PageSize = 20 }
func (c *OrdersConfig) Validate() error { ... }
```

- `config.Provide[T]("orders")` 把 YAML 的 `orders:` 段注入为 `*OrdersConfig`。
- 顺序：`SetDefaults` → 覆盖 YAML → `Validate`；非法值**启动失败**。
- `app validate` 会跑所有已登记段的校验；`Config.Unused()` 能发现拼错的键。

---

## 3. 从零到一

### Step 1：最小服务（能跑、有健康检查）

```go
package main

import "github.com/fitan/fxkit"

func main() {
    fxkit.Run(fxkit.Core())
}
```

```yaml
app:
  name: "hello"
server:
  port: "8080"
```

```bash
go run . serve -c configs/config.yaml
curl localhost:8080/healthz   # ok
curl localhost:8080/readyz    # {"status":"ok"}
curl localhost:8080/version
```

> `Core()` 已包含 HTTP listener；早期版本 `Minimal()` 不监听，现在已修。

### Step 2：加业务模块（裸 chi + Huma）

```go
// internal/hello/hello.go
var Module = fxkit.Service("hello",
    fxkit.Provide(func(di.Injector) (*Service, error) { return NewService() }),
    fxkit.Routes(newRoutes()...),
)

type Service struct{}

func NewService() (*Service, error) { return &Service{}, nil }

func newRoutes(s *Service) []server.Route {
    return []server.Route{{
        Pattern: "/hello",
        Methods: []string{http.MethodGet},
        Handler: http.HandlerFunc(s.hello),
    }}
}

func (s *Service) hello(w http.ResponseWriter, _ *http.Request) {
    _, _ = w.Write([]byte("hi"))
}
```

```go
// main.go
func main() {
    cli.SetRootName("hello", "hello service")
    fxkit.Run(
        fxkit.Core(),
        fxhuma.Module,                 // 需要 REST 时加
        hello.Module,
        fxhuma.ProvideRegistrar(registerOps),
    )
}

func registerOps(api huma.API) {
    fxhuma.Register(api, huma.Operation{
        OperationID: "getGreeting", Method: http.MethodGet, Path: "/greeting",
        Summary: "Say hello",
    }, func(ctx context.Context, in *greetingIn) (*greetingOut, error) {
        out := &greetingOut{}
        out.Body.Message = "hello " + in.Name
        return out, nil
    })
}

type greetingIn struct {
    Name string `query:"name" default:"world"`
}

type greetingOut struct {
    Body struct {
        Message string `json:"message"`
    }
}
```

> 用 `fxhuma.Register` 而不是 `huma.Register`：返回的 `error` 会被映射成统一错误体
> （`fxerrors`），并把 5xx 的内部细节隐藏、只记日志。裸 `huma.Register` 则会把 `error` 原样当 500 文本。

验证：

```bash
curl localhost:8080/hello
curl 'localhost:8080/greeting?name=zed'
```

### Step 3：接数据库 + 迁移

```yaml
db:
  driver: "postgres"
  dsn: "postgres://user:pass@localhost:5432/app?sslmode=disable"
  auto_migrate: false        # 生产由 SQL 平台建表
  check_schema: true         # 启动时校验，发布顺序错直接失败
```

```go
type Service struct{ db *gormx.Client }

func NewService(db *gormx.Client) (*Service, error) { return &Service{db: db}, nil }

func (s *Service) Create(ctx context.Context, o *Order) error {
    return s.db.Transaction(ctx, func(txCtx context.Context) error {
        return s.db.Conn(txCtx).Create(o).Error
    })
}
```

**建表（框架自己的 5 张表）**：

```bash
myapp migrate print --dialect postgres > fxkit-0001.sql   # 交给 SQL 平台执行
myapp migrate check -c configs/config.yaml                # 执行后校验 → schema OK
```

业务表由业务迁移管理；`db.auto_migrate: true` 只用于本地开发。

### Step 4：列表查询（crudx）

```go
var orderListSpec = crudx.ListSpec{
    Fields: map[string]crudx.FieldSpec{
        "id":     {Column: "orders.id", Kind: crudx.FieldNumber},
        "status": {Column: "orders.status", Kind: crudx.FieldString, Indexed: true},
        "title":  {Column: "orders.title", Kind: crudx.FieldString, Indexed: true, Likeable: true},
    },
    Relations: map[string]*crudx.RelationSpec{
        "user": {
            Table: "users", Alias: "u", On: "u.id = orders.user_id",
            Fields: map[string]crudx.FieldSpec{
                "name": {Column: "u.name", Kind: crudx.FieldString},
            },
            Cardinality: crudx.RelationToOne, // 多对一：跳过 DISTINCT
        },
    },
    SortFields:  map[string]string{"id": "orders.id", "created_at": "orders.created_at"},
    DefaultSort: "id",
    Limits:      crudx.Limits{LimitDefault: 20, LimitMax: 100},
}
```

Huma 侧（`fxhuma` 是 `github.com/fitan/fxkit/huma` 的惯用导入别名；输入必须是结构体，把
`ListQueryInput` 内嵌进去，Huma 才会把 `q=` / `sortBy=` / `cursor=` 解析成参数）：

```go
type listOrdersIn struct {
    fxhuma.ListQueryInput // q / sortBy / sortDirection / limit / start / cursor / useCursor / replyWithCount
}

type listOrdersOut struct {
    Body crudx.ListResult[OrderRow]
}

fxhuma.Register(api, huma.Operation{
    OperationID: "listOrders", Method: http.MethodGet, Path: "/orders",
}, func(ctx context.Context, in *listOrdersIn) (*listOrdersOut, error) {
    params, err := fxhuma.ListParamsFromInput(&in.ListQueryInput)
    if err != nil {
        return nil, err
    }
    res, err := crudx.List(ctx, crudx.ListInput[Order, OrderRow]{
        DB:     s.db.Conn(ctx).Model(&Order{}),
        Spec:   orderListSpec,
        Params: params,
        ToRow:  toOrderRow,
    })
    if err != nil {
        return nil, err
    }
    return &listOrdersOut{Body: res}, nil
})
```

> 想省掉这层样板就用 [`fxhuma.RegisterResource`](../huma/register_resource.go)：它一次性挂上
> `GET/POST /orders`、`GET/PUT/DELETE /orders/{id}` 五个路由，列表路由内部就是这个写法。

客户端协议：

```
GET /orders?q=status=paid&q=title~invoice&sortBy=created_at&sortDirection=desc&limit=20
GET /orders?cursor=<nextCursor>            # 翻页只带 cursor 即可（方向已锁定）
```

> 完整语法、游标边界、错误码见 [`crudx/README.md`](../crudx/README.md)。

### Step 5：鉴权（JWT + Casbin）

```yaml
auth:
  enabled: true
  issuer: "https://idp.example.com/"
  audience: "https://api.example.com"
  dev_header_user: false
  casbin:
    enabled: true
    bootstrap_role: "admin"
    bootstrap_users: ["alice"]
    auto_register_routes: true
```

登记需要权限的路由：

```go
authz.ProvideHTTPRoutes(func() []authz.HTTPRoute {
    return []authz.HTTPRoute{
        {Method: http.MethodGet, Path: "/orders", Permission: "orders:list"},
        {Method: http.MethodPost, Path: "/orders", Permission: "orders:create"},
    }
})
```

handler 里取身份：

```go
subj, ok := authz.SubjectFromContext(ctx)
if !ok { /* 理论上不会发生：中间件已拦截 */ }
_ = subj.ID
```

- 默认只覆盖 **Huma**。裸 chi 路由需 `auth.mux_middleware: true`（覆盖全部 mux 请求）或宿主自行加中间件。
- 匿名端点：Huma 用 `authz.OpPublic` 元数据；mux 模式用 `auth.public_paths`。
- 本地联调：`dev_header_user: true` + `curl -H 'X-User: alice'`。

### Step 6：异步（outbox + Hatchet）

**生产端**：业务写入与事件入队同事务。

```go
return s.db.Transaction(ctx, func(txCtx context.Context) error {
    if err := s.db.Conn(txCtx).Create(&order).Error; err != nil {
        return err
    }
    return outbox.EnqueueTopicMsg(txCtx, outbox.EnqueueTopicInput[OrderCreated]{
        Store: s.store,
        Topic: "order-created",
        Msg:   OrderCreated{ID: order.ID, Total: order.Total},
        IdempotencyKey: fmt.Sprintf("order-created:%d", order.ID),
    })
})
```

**消费端**（Hatchet task）：

```go
hatchetx.ProvideRegistrarLazy(func(i di.Injector) (hatchetx.Registrar, error) {
    svc := di.MustInvoke[*Svc](i)
    return func(c *hatchetx.Client) ([]hatchetx.WorkflowBase, error) {
        task := c.SDK.NewStandaloneTask(
            "order-created",
            svc.HandleOrderCreated,
            hatchet.WithWorkflowEvents("order-created"),
        )
        return []hatchetx.WorkflowBase{task}, nil
    }
})

func (s *Svc) HandleOrderCreated(ctx hatchet.Context, ev OrderCreated) (map[string]any, error) {
    return map[string]any{"ok": true}, s.inbox.Once(ctx, outbox.OnceInput{
        Key:   fmt.Sprintf("order-created:%d", ev.ID),
        Topic: "order-created",
        Fn: func(ctx context.Context) error {
            return s.apply(ctx, ev)   // 至少一次投递 → 这里保证只应用一次
        },
    })
}
```

**定时任务**：

```go
id, err := hatchetx.CreateReminder(ctx, client, hatchetx.CreateReminderInput{
    WorkflowName: "order-created",
    ActorID:      "scheduler",
    ReminderName: "heartbeat",
    Period:       "*/15 * * * * *",   // 6 位含秒
})
defer hatchetx.DeleteReminder(ctx, client, id)
```

**Actor（按 id 串行的邮箱）**：

```go
counter := hatchetx.NewActor("order-counter", func(ctx context.Context, in IncInput) (IncOut, error) {
    return IncOut{Total: in.Delta}, nil
})
hatchetx.ProvideRegistrar(counter.Registrar())
// 调用：counter.Call(ctx, client, IncInput{ActorRef: hatchetx.ActorRef{ActorID: "o-1"}, Delta: 1})
```

### Step 7：缓存 / 出站 HTTP / 分布式锁

```yaml
cache:
  driver: "redis"
  default_ttl: 30m
  null_ttl: 1m
  singleflight: true
```

```go
order, err := cachex.GetOrSet(ctx, cache, "order:"+id, func(ctx context.Context) (*Order, error) {
    return s.repo.Find(ctx, id)
})
```

出站 HTTP（Consul 服务发现 + failover）：

```go
users, err := s.factory.Client(reqx.ClientInput{Name: "users"})   // http://users/... 会被改写到实例
resp, err := users.R().SetContext(ctx).Get("/v1/users/" + id)
```

分布式锁：

```go
err := s.locker.WithLock(ctx, "orders/"+id, func(ctx context.Context) error {
    return s.process(ctx, id)
}, consulx.WithTTL(15*time.Second))
```

> 锁**不可重入**：同一 `Lock` 实例重复加锁返回 `ErrLockHeld`。
> 锁 key 只允许 `[a-zA-Z0-9,_./\-?&=]`，非法字符本地直接报 `ErrInvalidLockKey`。

### Step 8：可观测性

```yaml
otel:
  enabled: true
  endpoint: "collector:4317"
  protocol: "grpc"
  insecure: false
  sampling: "trace_id_ratio:0.1"
server:
  access_log: true     # 结构化访问日志
```

自动就有：

- **trace**：HTTP 进出（otelhttp）、每条 SQL（otelsql）、Hatchet step、`cache.load`、`mcpx.tool`、`outbox.publish`（并把 `traceparent` 写进事件 metadata）。
- **log**：slog 自动注入 `trace_id`/`span_id`；错误出口会记录内部错误。
- **metrics**：HTTP/DB/runtime + 9 个子系统业务指标（见 README §13）。

---

## 4. 常见业务模式

### 4.1 事务边界

```go
return db.Transaction(ctx, func(txCtx context.Context) error {
    // 所有写操作都用 db.Conn(txCtx)
})
```

- `AfterCommit(ctx, func(cbCtx context.Context){...})` 在提交后执行；回调里的 `Conn(cbCtx)` 走连接池（不是已结束的事务）。
- 裸 `WithTx` 没有提交边界，`AfterCommit` 会告警并立即执行。

### 4.2 幂等消费

- 生产端：`IdempotencyKey` 唯一，重复入队是 no-op。
- 消费端：`Inbox.Once`（key + fn 同一事务，失败不会标记 key）。

### 4.3 分页

- 小数据量/需要 total → `offset` + `replyWithCount=true`。
- 大数据量 → `cursor`（keyset），不要用深 offset。

### 4.4 健康检查

```go
server.ProvideHealthChecker(func(svc *Svc) server.HealthChecker {
    return svc   // 实现 Name()/Check(ctx)
})
```

`/healthz` = 存活；`/readyz` = 依赖聚合（并发执行，默认 2s 超时）。

---

## 5. 配置总览（速查）

| 段 | 关键键 | 默认 |
|---|---|---|
| `app` | `name` | — |
| `server` | `port` / `log_payloads` / `access_log` / `rate_limit.*` / `cors_*` / 超时 | 8080 / false / false / 关 |
| `db` | `driver` / `dsn` / `auto_migrate` / `check_schema` / 连接池 | 不连库 / false / false |
| `cache` | `driver` / `default_ttl` / `null_ttl` / `singleflight` / `jitter_max_percent` / `redis.*` | 禁用 / 30m / 1m / true / 10 |
| `outbox` | `enabled` / `poll_interval` / `batch_size` / `max_retries` / `claim_timeout` / `publish_concurrency` | false / 1s / 50 / 10 / 30s / 1 |
| `hatchet` | `enabled` / `token` / `host_port` / `server_url` / `tls_strategy` / `worker_*` / `outbox_publisher` | false |
| `auth` | `enabled` / `issuer` / `audience` / `dev_header_user` / `deny_unregistered` / `mux_middleware` / `public_paths` / `casbin.*` | false |
| `otel` | `enabled` / `endpoint` / `protocol` / `sampling` / `traces|metrics|logs.*` | false |
| `discovery` | `consul_address` / `register` / `advertise_address` / `tags` | 空 / false |
| `docs` | `enabled` | **false** |
| `mcp` | `enabled` / `name` / `path` / `headers` / `max_response_bytes` | **false** / 1 MiB |

完整示例见 README §4。

---

## 6. 测试

```bash
go test ./...                          # 单元
go test -race ./...                    # 并发
FXKIT_STRICT_WIRING=1 go test ./...    # 装配严格模式
```

集成测试（默认 skip，需 env）：见 [`docs/testing.md`](./testing.md)。
HTTP 端到端：`cd example && bash scripts/verify-local.sh`。

---

## 7. 上线

完整清单见 [`docs/production-checklist.md`](./production-checklist.md)。核心顺序：

```
migrate print → SQL 平台执行 → migrate check → 滚动发布（check_schema 再校验）
```

灰度：1 副本 → 10% → 50% → 100%，观察错误率 / P99 / outbox lag / 锁丢失。

---

## 8. 故障排查

| 症状 | 可能原因 | 处理 |
|---|---|---|
| 启动成功但路由 404 | 装配期读了 group（只拿到部分） | 开 `FXKIT_STRICT_WIRING=1` 定位，改为 PreStart 读取 |
| `relation/table does not exist` | DDL 未执行或顺序错 | `migrate print` → 平台执行；`db.check_schema: true` |
| `/readyz` 503 | DB/Redis 不可用 | 看 `server.readyz.checks{status="error"}` 的 `check` 维度 |
| 任务/worker 不执行 | `hatchet.enabled` 未开、token 无效、没有 registrar | 看启动日志 `hatchetx: worker started` |
| outbox 不投递 | 没有 `EventPublisher` | 开 `hatchet.outbox_publisher` 或 `outbox.ProvidePublisher` |
| 锁偶尔丢失 | 续租失败（网络/TTL 过短） | 看 `consulx.lock.renew.failures` / `lock.lost`，加大 TTL |
| 列表 400 `INVALID_SORT_FIELD` | 排序字段不在白名单，或与游标冲突 | 检查 `SortFields`；翻页只传 `cursor` |
| 列表 400 `DISTINCT_RELATION_SORT_UNSUPPORTED` | 一对多 JOIN 下按关联列排序 | 声明 `RelationToOne` 或改按根表列排序 |
| 缓存命中率异常 | 负缓存/类型不匹配 | 看 `cachex.lookups{result}` |
| 日志没有 trace_id | otel 未启用或宿主覆盖了 slog | 检查 `otel.enabled`；`logx` 不覆盖宿主 handler |

---

## 9. 反模式清单

| 反模式 | 后果 | 正确做法 |
|---|---|---|
| 在模块 `Option` 里 `di.GetGroup` | 只读到部分贡献，路由/worker 静默缺失 | 放 `Lifecycle.PreStart` |
| 生产开 `db.auto_migrate` | 多副本并发 DDL、不可控变更 | 平台执行 `migrate print` 的 DDL |
| 生产开 `docs.enabled` / `mcp.enabled` | API 结构暴露 | 默认关闭，按需 + 网关鉴权 |
| 生产开 `dev_header_user` | 任何人伪造 `X-User` | 必须 false |
| 依赖 Huma 的 `limit` 默认值 | 实际返回 100 而非你的 `LimitDefault` | 在 `ListSpec.Limits` 显式设，并让客户端传 |
| handler 里用 `db.Pool()` | 绕过 ctx / 事务 / trace | 用 `db.Conn(ctx)` |
| 忽略 `Unlock` 错误 | 锁丢失无人知晓 | `WithLock` 或检查 `ErrLockLost` |
| 把 `q=` 当 ORM | 复杂关联查询难维护 | 复杂查询写显式 SQL/方法 |

---

## 10. 索引

| 内容 | 位置 |
|---|---|
| 分章参考（配置、各包 API、迁移） | [`README.md`](../README.md) |
| 列表查询语法与错误码 | [`crudx/README.md`](../crudx/README.md) |
| 测试（含集成测试 env） | [`docs/testing.md`](./testing.md) |
| 上线清单（DDL/灰度/告警） | [`docs/production-checklist.md`](./production-checklist.md) |
| 完整示例（catalog + storefront） | [`example/README.md`](../example/README.md) |
| 迁移 SQL | [`gormx/migrations`](../gormx/migrations) |
| Atlas 配置（仅 lint/diff） | [`atlas.hcl`](../atlas.hcl) |
| AI 助手使用规范 | [`skills/`](../skills) |
