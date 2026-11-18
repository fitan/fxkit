# fxkit 横切能力缺口与解决方案

> 对应 `docs/guide.md` §11 与 `.agents/skills/fxkit-maintain` 的缺口清单。
> 决策记录与落地状态见 [`gap-solutions-decisions.md`](./gap-solutions-decisions.md)。
> 状态：方案草案（部分已实现，见决策记录）。
> 原则：**不过度设计、不过度防御**。不是所有缺口都进框架；先有第二个真实调用方，再抽象。

## 0. 归属与优先级

| # | 能力 | 归属 | 优先级 | 结论 |
|---|---|---|---|---|
| 1 | HTTP 幂等键 | 框架 opt-in 模块 | P1 | 建议做 |
| 2 | 乐观锁 / version | `gormx` helper | P1 | 建议做 |
| 3 | 审计日志 | 框架 opt-in 模块 | P1 | 建议做 |
| 4 | 多租户数据隔离 | 框架 helper | P1/P2 | 有真实多租户再做 |
| 5 | 限流粒度 | `server` 扩展 | P1 | 建议做 |
| 6 | 熔断 / 隔离 / load shedding | `reqx` / `server` opt-in | P2/P3 | 有流量再做 |
| 7 | DLQ / 毒消息 | `outbox` / `hatchetx` 扩展 | P1/P2 | 补管理面 |
| 8 | outbox/inbox 保留 | 配置 + 定时任务 | P2 | 已有手动 API，补自动/文档 |
| 9 | 配置热更新 | 不做 | P3 | 明确不支持 |
| 10 | 安全头 / CSRF | `server` opt-in | P2 | 安全头建议做 |
| 11 | pprof / profiling | `server` opt-in | P2 | 建议做 |
| 12 | 错误追踪 / SLO / 告警 | 外部平台 | P3 | 文档/集成，不进框架 |
| 13 | 缓存批量失效 | `cachex` 扩展 | P2 | 先做 prefix 删除 |
| 14 | 读写分离 / 分库分表 | 外部 / P3 | P3 | 有规模再做 |
| 15 | API 版本化 | 约定 | P2 | 文档约定，不做机制 |
| 16 | i18n | 业务层 | 不做 | 用错误码映射 |

**归属说明**

- **框架 core**：所有服务都需要、且抽象稳定（限流键、安全头、pprof）。
- **opt-in 模块**：有用但不应默认开启（幂等、审计、熔断）。
- **外部/网关**：SLO、告警、错误追踪、TLS、WAF、CSRF（cookie 场景除外）。
- **业务/领域**：支付、通知、搜索、文件存储、i18n、报表。

---

## 1. HTTP 幂等键

### 问题

客户端超时重试 `POST/PUT/PATCH/DELETE`，可能重复下单、重复扣款。现有 `outbox/inbox` 只解决**事件消费**幂等，不覆盖 HTTP 入口。

### 设计

- 只在请求带 `Idempotency-Key` 时生效；不自动生成 key。
- 只对 mutating method 生效；GET/HEAD 直接放行。
- 键 = `Idempotency-Key`；值 = `method + path + body hash`。
- 状态：
  - 无记录 → 执行并记录响应。
  - 已完成且 hash 相同 → 重放响应，加 `Idempotent-Replayed: true`。
  - 已完成但 hash 不同 → `409 IDEMPOTENCY_KEY_REUSED`。
  - 处理中 → `409 IDEMPOTENCY_IN_PROGRESS`（或按配置短暂等待）。
- 只缓存 **2xx 和 4xx**；5xx 不缓存，允许重试。

### 接口

```go
package idempotency

type Record struct {
	RequestHash string
	StatusCode  int
	Body        []byte
	Completed   bool
}

type Store interface {
	Get(ctx context.Context, key string) (Record, bool, error)
	Begin(ctx context.Context, key, reqHash string, ttl time.Duration) (Record, bool, error)
	Complete(ctx context.Context, key string, status int, body []byte, ttl time.Duration) error
	Abort(ctx context.Context, key string) error
}

func Middleware(cfg Config, store Store) server.Middleware
```

### 配置

```yaml
idempotency:
  enabled: false
  header: "Idempotency-Key"
  ttl: 24h
  methods: [POST, PUT, PATCH, DELETE]
  max_body_bytes: 1048576
  store: "db"        # db | redis
```

### 实现位置

- 新包 `idempotency/`。
- `DBStore`：`idempotency_keys` 表（key、request_hash、status_code、response_body、created_at、expires_at）。
- `RedisStore`：需要 `SET NX`；当前 `cachex.Cache` 没有 SetNX，需扩展接口或直接用 Redis 客户端。
- 中间件挂在 `server.ProvideMiddleware`，确保在 authz 之后、业务 handler 之前；`server.ProvideMiddleware` 的顺序 = Provide 顺序。

### 迁移 / 测试

- 新增表由宿主 DDL 平台执行；框架提供 `migrate print` 片段。
- 测试：重放、同 key 不同 body、并发同 key、过期、5xx 不缓存。
- 文档/skill：`docs/guide.md` + `skills/fxkit`（或新 `fxkit-idempotency`）。

### 优先级

P1。建议先做 DB store；Redis store 有性能需求再加。

### 疑问
Idempotency-Key  是前端生成吗？  
outbox/inbox  是不是 也可以有效果  如果 id 重复 自然 就返回错误了。 

---

## 2. 乐观锁 / version

### 问题

并发更新同一行时，后写覆盖先写。GORM 没有统一 version 约定。

### 设计

- 模型加 `Version int64 `gorm:"not null;default:1"``。
- 更新语句带 `WHERE id=? AND version=?`，成功后 `version=version+1`。
- `RowsAffected == 0` → `fxerrors.Conflict("version conflict")`。
- 不引入 GORM 插件依赖；手写 helper。

### 接口

```go
package gormx

type Versioned interface {
	GetVersion() int64
	SetVersion(int64)
}

// UpdateWithVersion 执行乐观锁更新；冲突返回 fxerrors.Conflict。
func UpdateWithVersion(ctx context.Context, db *Client, model Versioned, fields ...string) error
```

### 使用

```go
next.Version = req.Version
if err := gormx.UpdateWithVersion(ctx, s.db, &next, "title", "body"); err != nil {
	return Detail{}, err
}
```

### 配置

无。显式 opt-in，不给所有模型自动加字段。

### 迁移 / 测试

- 现有表加 `version` 列；旧数据默认 1。
- DTO 增加 `version`；更新请求必须带。
- 测试：两个并发更新，一个成功一个 409。
- 文档/skill：`skills/fxkit-huma-crud` 写操作章节。

### 优先级

P1。

### 这个需求 常见吗？  感觉是小概率事件？ 

---

## 3. 审计日志

### 问题

只有 access log，缺少“谁在什么时候对什么做了什么”的合规审计。

### 设计

- 中间件采集：时间、Actor、OrgID、Method、Path、Status、RequestID、TraceID、客户端 IP。
- 默认不记录 body；需要时按配置开启并脱敏。
- Sink 可插拔：DB 表、outbox topic、slog。
- 业务级审计（如“修改了价格”）由业务显式写入，不自动推断。

### 接口

```go
package audit

type Event struct {
	At        time.Time
	Actor     string
	OrgID     string
	Method    string
	Path      string
	Status    int
	Action    string
	Resource  string
	RequestID string
	TraceID   string
	Metadata  map[string]any
}

type Sink interface {
	Write(ctx context.Context, ev Event) error
}

func Middleware(cfg Config, sink Sink) server.Middleware
```

### 配置

```yaml
audit:
  enabled: false
  include_body: false
  redact_headers: [Authorization, Cookie]
  sink: "db"          # db | outbox | slog
  retention: 720h
```

### 实现位置

- 新包 `audit/`。
- `DBSink`：`audit_events` 表。
- `OutboxSink`：`outbox.EnqueueTopicMsg`，异步消费到审计库。
- 使用 `authz.SubjectFromContext` 和 OTel trace ID。
- 顺序：审计中间件必须在 authz 之后，否则拿不到 Actor/OrgID。

### 迁移 / 测试

- 新表 + 保留策略（可复用 outbox 的 `PurgePublished` 思路）。
- 测试：字段填充、脱敏、sink 失败不阻断业务（或按配置阻断）。
- 文档/skill：新 `fxkit-audit` 或并入 `fxkit`。

### 优先级

P1。默认关闭；合规场景再开。

###  建议
这个可以加上 我批准了

---

## 4. 多租户数据隔离

### 问题

`authz` 已有 Casbin domain / `Subject.OrgID`，但数据层不会自动带 tenant 条件，容易漏过滤。

### 设计

- 认证后把 `OrgID` 写入 context。
- 查询显式使用 `tenant.Scope(ctx, db)`；不做全局自动 scope（容易被绕过）。
- 写入显式设置 `org_id`；可选 GORM `BeforeCreate` hook，但默认不开启。
- 启用 `tenant.enabled` 且 context 无 tenant 时，`Ensure` 直接报错（fail closed）。

### 接口

```go
package tenant

func WithTenant(ctx context.Context, id string) context.Context
func FromContext(ctx context.Context) (string, bool)
func Ensure(ctx context.Context) (string, error)
func Scope(ctx context.Context, db *gorm.DB, column string) *gorm.DB
```

### 配置

```yaml
tenant:
  enabled: false
  column: "org_id"
  claim: "organization_id"
```

### 实现位置

- 新包 `tenant/` 或 `authz/tenant`。
- 限流键、审计、日志都可复用 tenant context。

### 迁移 / 测试

- 现有表加 `org_id` 并回填；查询逐处加 `tenant.Scope`。
- 测试：跨租户查不到、缺失 tenant 报错。
- 文档/skill：新 `fxkit-tenant` 或并入 `fxkit-authz`。

### 优先级

P1/P2。只有真实多租户需求再做；单租户不要引入。
### 疑问
如果实现了多租户 在 单租户的情况 应该怎么 使用呢？  引入了会不会 增加项目的复杂度。

---

## 5. 限流粒度

### 问题

当前 `server.rate_limit` 只按客户端 IP 的令牌桶；没有 per-route / per-tenant / per-user。

### 设计

- 扩展 `RateLimitConfig`：`key_by`、`per_route`。
- 默认仍按 IP；可选 `subject`、`tenant`、`route`、`header:<name>`。
- 分布式场景可选 Redis 存储；默认进程内令牌桶。
- 顺序约束：当前 `rateLimit` 在 authz 之前执行；`key_by: subject/tenant` 需要把限流移到 authz 之后（或由 authz/tenant 模块提供后置限流），否则 context 里没有身份。

### 配置

```yaml
server:
  rate_limit:
    enabled: true
    rps: 20
    burst: 40
    key_by: "ip"        # ip | subject | tenant | route | header:X-API-Key
    per_route: false
```

### 实现位置

- `server/ratelimit.go`：key 函数从 context / chi route pattern 取。
- Redis 版可放 `server` 或独立 `ratelimit` 包；不要在 `server` 里硬依赖 Redis。

### 测试

- 不同 key 隔离、同 key 命中、per-route 独立桶。
- 文档/skill：`skills/fxkit` + `config-checklist.md`。

### 优先级

P1。

### 建议 
可以加上 我批准了 

---

## 6. 熔断 / 隔离 / load shedding

### 问题

下游故障时，`reqx` 只有 failover，没有熔断；服务端没有最大并发限制。

### 设计

- `reqx` 增加简单 breaker：closed → open → half-open；按 `ClientInput.Name` 维度。
- `reqx.max_concurrency`：每个下游一个信号量（bulkhead）。
- `server.max_inflight`：超过直接 503，保护自身。
- 不引入完整 resilience 库；先做最小状态机 + semaphore。

### 配置

```yaml
reqx:
  breaker:
    enabled: false
    failure_threshold: 5
    open_timeout: 10s
    half_open_max: 1
  max_concurrency: 100

server:
  max_inflight: 0     # 0 = 不限制
```

### 实现位置

- `reqx/breaker.go`：`http.RoundTripper` 包装。
- `server`：in-flight 中间件。

### 测试

- 状态转换、半开恢复、并发上限。
- 文档/skill：`skills/fxkit-reqx` + `skills/fxkit`。

### 优先级

P2/P3。有真实下游故障/流量再做。
### 建议
先不用做

---

## 7. DLQ / 毒消息

### 现状

- `outbox` 已有 `StatusFailed`、`RequeueFailed`、`PurgePublished`。
- `hatchetx` 有 `WithRetries` / `WithRetryBackoff`；Hatchet 控制台可看失败 run。
- 缺：统一的管理面（列表、重放、清理）、inbox 的 stale/failed 清理。

### 设计

- `outbox` 增加管理 API/CLI：
  - `ListFailed(filter)` / `RequeueFailed(filter)`（已有）/ `PurgeFailed(before)`。
- `hatchetx` 增加 `ListFailedRuns` 薄封装（若 SDK 支持）；否则文档指向 Hatchet 控制台。
- `inbox` 增加 `PurgeDone(before)` 和 `ListStale(before)`。
- 不新建 DLQ 服务；复用现有表/控制台。

### 配置

```yaml
outbox:
  failed_retention: 168h   # failed 行保留期；0 = 不自动清理
  inbox_retention: 168h    # done 行保留期；0 = 不自动清理
```

### 测试

- failed 列表/重放/清理边界；inbox stale。
- 文档/skill：`skills/fxkit-hatchet`。

### 优先级

P1/P2。先补管理面，不急着自动清理。

### 建议
可以做。  但是做成sdk 集成的方式 。 

---

## 8. outbox/inbox 保留策略

### 现状

`Store.PurgePublished` 已存在，但明确“框架不自动清理”，由宿主定时任务调用。`inbox` 没有 purge。

### 设计

- 保持“默认不自动清理”，避免误删审计数据。
- 提供可选自动清理：
  - `outbox.purge_interval` + `outbox.purge_before`。
  - `inbox.purge_interval` + `inbox.purge_before`。
- 在 `di.Lifecycle` 启动时挂一个 goroutine；`Stop` 时退出。
- 默认关闭；开启后日志打印删除行数。

### 配置

```yaml
outbox:
  purge_interval: 0s       # 0 = 关闭
  purge_before: 168h
  inbox_purge_before: 168h
```

### 测试

- 边界时间、只删 published/done、不删 pending/failed。
- 文档/skill：`config-checklist.md` + `skills/fxkit-hatchet`。

### 优先级

P2。先文档说明手动清理；有表膨胀再加自动。

### 建议 
可以加上 但是注意 并发的 自动处理 

---

## 9. 配置热更新

### 结论

**明确不支持**。`config.Config` 只在启动时构建；`ProvideConfig` 注入的 `*T` 是快照。

### 建议

- 文档写清：改配置 = 重启/滚动发布。
- 宿主如确有需求，自行实现：新建 `config.Config` → `Load` → 重建依赖；不要指望框架自动刷新。
- 不引入 `atomic.Value` 全局配置总线；这会把“配置快照”变成隐式可变状态，违反单一事实来源。

### 优先级

### 建议 我觉得可以引入。 可以依赖 consul 然后 根据 key value  watch。 每次 read 的时候 可以保持最新的 

P3 / 不做。

---

## 10. 安全头 / CSRF

### 设计

- `server.security_headers` 中间件：
  - `X-Content-Type-Options: nosniff`
  - `X-Frame-Options: DENY`
  - `Referrer-Policy: no-referrer`
  - `Permissions-Policy`（可选）
  - `Strict-Transport-Security`（仅 HTTPS，显式开启）
- CSRF：Bearer token API 不需要；仅 cookie 认证场景加 double-submit cookie 中间件。

### 配置

```yaml
server:
  security_headers:
    enabled: true
    hsts: false
    csp: ""
```

### 实现位置

- `server/middleware.go`。
- 默认开启安全头；HSTS/CSP 显式配置。

### 测试

- 头存在、CSP 为空不输出、HSTS 仅 HTTPS。
- 文档/skill：`config-checklist.md` + `docs/production-checklist.md`。

### 优先级
### 疑问 可以给我讲讲 作用 。

P2。安全头建议做；CSRF 视认证方式。

---

## 11. pprof / profiling

### 设计

- `server.pprof.enabled` 默认 false。
- 注册 `/debug/pprof/`，走 authz；或挂到独立 admin listener。
- 生产只在内网/临时开启。

### 配置

```yaml
server:
  pprof:
    enabled: false
    prefix: "/debug/pprof/"
```

### 实现位置

- `server`：`net/http/pprof` 注册。
- 文档：`docs/production-checklist.md` 写清开启方式与风险。

### 优先级

### 建议
加上 可以做 

P2。

---

## 12. 错误追踪 / SLO / 告警

### 结论

不进框架。OTel 已经把 traces/metrics/logs 导出；错误追踪、SLO、告警交给 SigNoz / Grafana / Sentry / 云监控。

### 框架侧要做

- 统一 metric 名称、span 名称、错误属性（`error.kind`、`http.route`）。
- 在 `docs/production-checklist.md` 给出推荐面板/告警项：
  - 5xx 比例、P99、outbox lag、inbox busy、锁丢失、breaker open、rate limit 429。

### 优先级

### 意见 不做。  但是 可以在文档中写 建议的 告警策略

P3 / 文档 + 外部平台。

---

## 13. 缓存批量失效

### 现状

`cachex.Cache` 只有 `Get/Set/Delete/MGet/MSet`；没有 prefix/tag 失效。

### 设计

- 先加 `DeletePrefix(ctx, prefix)`：Redis `SCAN` + `DEL`，明确 O(N)，只用于低频管理操作。
- 暂不做 tag 系统；需要时再设计（tag → key set）。
- 业务缓存 key 统一用 `cachex.Prefix` + 资源前缀，便于批量删。

### 接口

```go
type PrefixDeleter interface {
	DeletePrefix(ctx context.Context, prefix string) (int64, error)
}
```

### 测试

- prefix 匹配、不误删、空 prefix 拒绝。
- 文档/skill：`skills/fxkit`。

### 优先级

### 疑问
有什么用 一般 不是 有过期时间吗？  

P2。

---

## 14. 读写分离 / 分库分表

### 设计

- 读写分离：`gormx` 增加 `db.replicas`，`Conn(ctx)` 默认主库；`gormx.Read(ctx)` 标记只读上下文，选择副本。
- 分库分表：**不做**。用外部中间件（Vitess / ShardingSphere）或业务层分片。
- 不在框架内实现路由/事务跨库。

### 配置

```yaml
db:
  replicas:
    - dsn: "postgres://replica1/..."
    - dsn: "postgres://replica2/..."
```

### 优先级
### 建议 
不做。 靠外部的中间件实现

P3。有真实读压力再做。

---

## 15. API 版本化

### 设计

- 约定：对外路径 `/v1/...`；`docs.BasePath` / `huma` path 统一。
- `authz.HTTPRoute` 带版本；`OperationID` 不重复。
- 弃用策略：旧版本保留 N 个迭代，文档标注 sunset。
- 不引入版本协商中间件/header 机制；路径前缀足够。

### 文档/skill

- `docs/guide.md` + `skills/fxkit-huma-crud`。
- `openapi.base.yaml` 的 `info.version` 与路径版本保持一致。

### 优先级

### 建议
不做

P2 / 约定，不做代码。

---

## 16. i18n

### 结论

业务层职责。框架只保证**机器可读错误码**：

- `fxerrors.Error.Kind` 已是稳定 code（`not_found` / `validation` / `conflict` …）。
- 客户端按 `Kind` + `Details` 映射本地化文案。
- 不在框架里维护翻译表；不把中文/英文文案写进 `fxerrors`。

### 优先级

### 建议
暂时不做

不做。

---

## 17. 建议路线图

### 第一批（P1，优先做）

1. HTTP 幂等键（DB store）
2. 乐观锁 helper
3. 审计日志（DB/outbox sink）
4. 限流 key 扩展（ip / subject / tenant / route）
5. pprof + 安全头（低风险、收益直接）

### 第二批（P2，按需）

1. 多租户 helper（有真实需求再做）
2. 熔断 / bulkhead / max_inflight
3. outbox failed 管理 + inbox purge
4. 缓存 prefix 删除
5. API 版本化约定文档

### 第三批（P3，观察）

1. 读写分离
2. gRPC / GraphQL / WebSocket
3. 更细的限流（Redis 分布式）
4. SLO/告警集成

### 不做

- 支付、通知、搜索、文件存储、i18n、报表。
- 全自动多租户 scope。
- 配置热更新。
- 全功能 API 网关。
- 分布式事务编排器（outbox + Hatchet 已够）。

---

## 18. 每项落地的统一要求

任何一项进入框架前，必须满足：

- [ ] 有第二个真实调用方（不是“将来可能”）。
- [ ] 有稳定的接口/配置，不依赖具体业务。
- [ ] 默认关闭或保持向后兼容。
- [ ] 有失败路径/并发测试。
- [ ] 文档 + 使用 skill 同步更新。
- [ ] 不引入新依赖，除非说明不可替代性。

参考 `.agents/skills/fxkit-maintain/SKILL.md` 的「优化循环」「决策规则」「验证矩阵」。
