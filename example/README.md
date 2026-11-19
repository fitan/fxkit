# fxkit showcase（example）

独立 Go module（`replace github.com/fitan/fxkit => ../`）。用 **catalog + storefront** 把 `fxkit.Default()` 里的包都跑一遍（`docs` 换成 `docs.MergedModule`，否则 `/docs` 仍是占位骨架）。前端是 **Nuxt 4 + Nuxt UI** dashboard 模板（`web/`，基于 `gh:nuxt-ui-templates/dashboard`）。

```
Browser → Traefik :8880
            ├─ /                   Nuxt UI dashboard（反代 :3000）
            ├─ /catalog/…          StripPrefix → catalog（Consul LB）
            └─ /storefront/…       StripPrefix → storefront → reqx → catalog
```

## 覆盖对照

| 包 | 例子里怎么测 |
|----|----------------|
| `fxkit` Default / Service / ProvideConfig / Routes | catalog `article` + `comment` + `system`；storefront `digest` |
| `cli` serve / openapi / version / AddCommand | `catalog doctor`；`make openapi SVC=catalog`；平台页能力矩阵 |
| `config` YAML + Consul KV + `--port` + `config.Watch` | replica 2：`--consul` + `--consul-key`；`--port 8083`；`/demo/config` 读 `config.Dynamic` 快照（demo 脚本把 `demo` 段放进 Consul KV，改 KV 即热更新） |
| `server` chi Route / PrefixRoute / ProvideMiddleware / CORS / health / 安全头 / pprof | `/chi/*`、`X-Fxkit-Chi`、CORS OPTIONS、`/healthz` `/readyz`；`server.security_headers` 默认开启；pprof 默认关闭；限流交给网关 |
| `fxerrors` | chi `/chi/errors/{kind}`（平台页探针）；Huma duplicate title 409 |
| `audit` | demo 开启（`sink: slog`）；db/outbox sink 可选 |
| `idempotency` | demo 开启；`POST /payments` 带 `Idempotency-Key` 重放同一响应（key 按 method/path/subject 作用域） |
| `gormx` | Postgres + `Transaction` + AutoMigrate；文章页事务创建；`UpdateWithVersion`（文章更新带 version，冲突 409）；`payments.Create` 用 `AfterCommit` 做提交后副作用 |
| `ledger` / `payment` | `payments/`：复式记账 + 状态机 CAS；`/payments/*` 演示 authorize/capture/refund（支持多次部分退款）/ledger |
| `webhook` / `reconciliation` | `/payments/webhook` HMAC 验签 + inbox 去重；`/payments/reconcile` 对账 |
| `cachex` | `GET /demo/cache/{name}` 用 `cachex.GetOrSet`（singleflight + TTL 抖动）；平台页 `GET /cache-demo` 仍是 Get/Set |
| `buildinfo` | `/version` JSON（Makefile ldflags）；平台页探针 |
| `logx` / `otelx` | slog + OTLP gRPC `10.170.34.223:4317`（insecure）；Hatchet worker 挂 `hatchet.start_step_run`（`hatchet.otel`） |
| `huma` RegisterResource + Register + ProvideMiddleware | 文章五件套；评论页 CRUD；平台页 `X-Fxkit-Huma` |
| `crudx` | 文章/评论页 q=`~` / `or()` / `.isnull` / JOIN / cursor / MapDBError / GetByID |
| `authz` | 未登录 401、JWT（可选）、alice admin、bob reader 403、charlie 无角色、`/authz/*`、`public_paths`、`SubjectFromContext`；平台页 authz 管理探针；storefront 与 catalog 共用 Postgres 策略表 |
| `hatchetx` + `outbox` | 文章页创建后 outbox → Hatchet → indexed + fanout；`/demo/hatchet/{schedule,dag,rate-limit}` 演示 ScheduleOnce / DAG / 原生限流；`/demo/outbox/{failed,requeue}` 演示 DLQ；`outbox.purge_*` 自动清理 |
| `consulx` | `make demo` 注册到 Consul；Traefik 自动发现；`POST /demo/lock/{name}` 演示分布式锁 + fencing token |
| `reqx` | 摘要页：storefront `NewFromFactory`（Consul watch + failover + OTel） |
| `docs` / `openapi` | MergedModule `/openapi.yaml`；Go `fxkit gen client`；前端 `openapi-typescript` + `openapi-fetch`（`make gen-web`）；平台页 OpenAPI 探针 |
| `mcpx` | demo 开启（`mcp.enabled: true`）；`/mcp/sse` 暴露 Huma operations 为 tools（默认关闭） |
| `cmd/fxkit` | `make gen-client` |

`fxkit.Minimal()` 等于 `Core()`，会监听端口，但不含 docs、数据库和鉴权。本例用 `Default()`。

## `/demo/*` 能力用例

`catalog/internal/demo/` 把可选能力做成可运行的 HTTP 用例；依赖未配置时对应接口返回 503，而不是静默成功。

| 接口 | 能力 |
|---|---|
| `GET /demo/cache/{name}` | `cachex.GetOrSet`：singleflight 防击穿、TTL 抖动、`Loads` 观察回源次数 |
| `GET /demo/config` | `config.Watch` / `config.Dynamic[DemoConfig]` 当前快照 |
| `POST /demo/lock/{name}` | `consulx.Locker` + `FencingToken` |
| `GET /demo/outbox/failed` | outbox DLQ 列表 |
| `POST /demo/outbox/requeue` | 把 failed 行重置为 pending 重投 |
| `POST /demo/hatchet/schedule` | `hatchetx.ScheduleOnce`（一次性） |
| `POST /demo/hatchet/dag` | 两步 DAG（`WithParents`） |
| `POST /demo/hatchet/rate-limit` | `UpsertRateLimit` |
| `POST /demo/tx?fail=true` | `gormx.AfterCommit`：提交才执行，回滚不执行 |

`make demo` 会把 `demo` 段写入 Consul KV `fxkit/demo/catalog`，并用 `--consul` 启动 catalog：

```bash
# 改 KV，无需重启；随后 GET /demo/config 立即看到新值
curl -X PUT --data-binary $'demo:\n  welcome: "changed"\n' \
  http://127.0.0.1:8500/v1/kv/fxkit/demo/catalog
```

## 运行

需要本机 Go 1.26+、Docker 与 Node 22+/pnpm。Go 依赖走 `GOPROXY=https://goproxy.cn,direct`；前端依赖走 `corepack pnpm install`。

```bash
cd example
make tidy && make build
make edge                 # Traefik + Consul + Redis（docker compose）
make web-install          # 首次：安装 Nuxt 前端依赖
make demo                 # PostgreSQL catalog/storefront + Consul + Traefik + Redis
make web-dev              # 另开终端：Nuxt dev :3000，API 经 Traefik :8880
make web-dev-direct       # 备用：Nuxt dev :3000，API 直连 8081/8082
make verify               # 完整链路：2×catalog + storefront + Traefik；结束后服务继续跑
make verify-local         # SQLite 单机端到端冒烟（不需要 Docker/Postgres/Consul）
make demo-down            # 停掉 demo 的 catalog/storefront + Postgres/Consul/Traefik/Redis
make down                 # 停掉所有 catalog/storefront + Traefik
```

`make demo` 使用 **PostgreSQL**（docker compose 的 `postgres` 服务，`fxkit/fxkit@127.0.0.1:5432`），数据保存在 `fxkit-postgres` volume。
`make verify-local` 仍使用 SQLite，只作为“无 Docker/Postgres 依赖”的冒烟测试，不是 demo 数据路径。

旧版 demo 的 `example/tmp/*.db` 不再被使用；如需清空 Postgres 数据，执行 `docker compose down -v` 后再 `make demo`。

迁移：`make migrate-baseline` 生成 Atlas 基线，随后用 `make atlas-status/lint/apply`；详见 [`migrations/README.md`](migrations/README.md) 与 [`docs/migrations-atlas.md`](../docs/migrations-atlas.md)。

浏览器：<http://127.0.0.1:8880>（Traefik → Nuxt）或 <http://127.0.0.1:3000>（直连 Nuxt）。Traefik Dashboard：<http://127.0.0.1:8881>，Consul UI：<http://127.0.0.1:8500>。前端源码在 `web/app`：首页/文章/评论/摘要/平台/设置均为中文；`make web-dev` 时 BFF 也经 Traefik（`NUXT_CATALOG_BASE`/`NUXT_STOREFRONT_BASE` 指向 8880），直连调试用 `make web-dev-direct`。

本地身份：`X-User: alice`（Casbin bootstrap admin）、`bob`（reader，只能 GET 文章/评论）、`charlie`（无角色 → 403）。生产请关 `auth.dev_header_user`。

## Web 页面（全中文）

| 页面 | 演示的框架能力 |
|------|----------------|
| `/` | 文章统计、最近文章（catalog） |
| `/articles` | huma + crudx + gormx：列表 / 搜索 / 创建 / 删除 |
| `/comments` | crudx JOIN（`article.title`）、评论 CRUD |
| `/digest` | reqx：storefront → catalog |
| `/payments` | 支付：幂等创建、授权/扣款、部分退款、账本、对账、webhook 模拟 |
| `/demo` | 可选能力：cache GetOrSet、动态配置、分布式锁、outbox DLQ、Hatchet、AfterCommit |
| `/platform` | server 健康探针、fxerrors、chi、hatchetx/outbox（含 heartbeat Actor）、cachex（Redis）、authz 管理、MCP、docs/openapi、能力矩阵 |
| `/settings` | Logto / X-User / JWT sub 绑定 |

## Traefik / Consul 发现

- `deploy/traefik/dynamic.yml` 只保留 Nuxt 路由和 `catalog-strip` / `storefront-strip` 中间件。
- catalog/storefront 启动时注册到 Consul，并带 `traefik.enable=true`、router rule、middlewares tags；Traefik 的 `consulCatalog` provider 自动生成 `/catalog/...`、`/storefront/...` 路由。
- `make demo` 用 `advertise_address: ""` 自动探测本机 LAN IP；宿主机上的 storefront reqx 和 Traefik 容器都能访问。
- 完整模式 `make verify` 用 2×catalog + Consul LB。

## Logto 控制台（Nuxt 登录）

`web/` 是 Nuxt UI dashboard，Logto 登录已接入 `@logto/browser`。`make web-dev` 默认在 `:3000`；经 Traefik 时是 `:8880`。重定向 URI 按当前 origin 生成，所以两个 origin 都要登记。`appId` = `2h3jba3gurf7r3vsvqydb`，endpoint = `https://10.170.34.223:3001/`。

打开 Logto Console，对这个应用填：

| 项 | 值（必须完全一致） |
|----|-------------------|
| 重定向 URI | `http://127.0.0.1:3000/callback`、`http://localhost:3000/callback`、`http://127.0.0.1:8880/callback`、`http://localhost:8880/callback` |
| 退出登录后重定向 URI | `http://127.0.0.1:3000/`、`http://localhost:3000/`、`http://127.0.0.1:8880/`、`http://localhost:8880/` |
| CORS allowed origins | 若 token 交换报 CORS，显式加 `http://127.0.0.1:3000`、`http://localhost:3000`、`http://127.0.0.1:8880`、`http://localhost:8880` |

再用局域网 IP 打开页面时（例如 `http://10.170.34.223:8880`），把对应 origin 的 `/callback` 和 `/` 也加进去。

**API Resource（拿不到 Bearer 就 401/403）：**

1. API resources → 创建，**Identifier** 必须是 `https://fxkit.showcase/api`（与 catalog/storefront 的 `auth.audience` 一致）。
2. 回到该应用 → API resources → 勾选这个 API。
3. 可选：设为 Default API resource，避免拿到 opaque token。

**浏览器信任自签证书：** 先用**同一个浏览器**打开 <https://10.170.34.223:3001/>，点继续访问。否则 SDK 拉 OIDC discovery / token 会被证书拦住。

登录后 JWT 的 `sub` 是 Logto 用户 id，不是 `alice`。第一次调 `/catalog/articles` 会 **403**。点右上角用户菜单的 **Bind JWT sub to admin**（或 Settings → General），用 `X-User: alice` 调 `PUT /authz/subjects/{sub}/roles`；或：

```bash
SUB='<页面上显示的 sub>'
curl -X PUT -H 'X-User: alice' -H 'Content-Type: application/json' \
  -d '{"roles":["admin"]}' \
  "http://127.0.0.1:8081/authz/subjects/${SUB}/roles"
```


Hatchet：token 放 `example/tmp/hatchet.token`（gitignore，对应本机 `localhost:7077`）。`make verify` 会自动读这个文件。

```bash
# 或
export HATCHET_CLIENT_TOKEN='...'
```

Hatchet gRPC 直连 `10.170.34.223:7077`（token 放 `example/tmp/hatchet.token`；token 里常写 localhost，那是 lite 默认声明，连远程时仍用 `hatchet.host_port`）。REST（定时运行/提醒器）用 `hatchet.server_url`，demo 默认 `http://10.170.34.223:8088`；`127.0.0.1:8080` 是 Kite/控制台，不是 Hatchet REST API。明文：`hatchet.tls_strategy: none`。Logto 自签：`auth.tls_insecure: true`。OTLP 直连 `10.170.34.223:4317`（gRPC, insecure）；本地调试可改回 `localhost:4317`。

验证 Actor 邮箱（丢更新 + 同 id 排队 inflight=1 + 异 id 重叠 inflight=2）：

```bash
CATALOG_URL=http://127.0.0.1:8081 bash scripts/verify_actor.sh
```

有真实 access token 时：

```bash
export LOGTO_ACCESS_TOKEN='...'
make verify
```

`aud` 须为 `https://fxkit.showcase/api`。

## 联调摘录

```bash
./bin/catalog doctor -c services/catalog/configs/config.yaml

curl http://127.0.0.1:8081/healthz
curl http://127.0.0.1:8081/version
curl http://127.0.0.1:8081/public/ping          # 匿名
curl -H 'X-User: alice' http://127.0.0.1:8081/chi/echo?msg=hi  # 裸 chi，仍经 authz
curl -H 'X-User: alice' http://127.0.0.1:8081/me
curl -H 'X-User: bob' http://127.0.0.1:8081/articles
curl -H 'X-User: alice' http://127.0.0.1:8081/authz/roles
curl -H 'X-User: alice' http://127.0.0.1:8081/openapi.yaml | head
curl -H 'X-User: alice' http://127.0.0.1:8082/digest
```

更新 typed 客户端：

```bash
# 1) Go 服务从 Huma 导出 OpenAPI
make openapi SVC=catalog   # 需要可达的 Postgres；hatchet.enabled 时还要 token
make openapi SVC=storefront

# 2) 生成 Go client（storefront 调 catalog）
make gen-client

# 3) 生成前端 TS 类型（openapi-fetch 使用）
make gen-web               # → web/app/types/{catalog,storefront}.d.ts
```

前端调用示例（`web/app/composables/useFxkit.ts` 里统一注入 `X-User` / `Bearer`）：

```ts
const { catalog, storefront } = useFxkitClients()
const { data } = await catalog.GET('/articles', {
  params: { query: { limit: 20, sortBy: 'id', sortDirection: 'desc', replyWithCount: true } }
})
const digest = await storefront.GET('/digest', { params: { query: { limit: 10 } } })
```

## 已知缺口

1. **Hatchet token** 未提供且 `tmp/hatchet.token` 不存在时，`make verify` 会关掉 hatchet/outbox；actor / reminder / indexed 会 SKIP。
2. **`fxkit.Default()` 的 `/docs` 是占位**。本例用 `docs.MergedModule` 换成 live spec。
3. `make demo` / `make edge` 会启动 Consul dev（`:8500`）；完整模式第二副本用 Consul KV 拉同一份 YAML。
4. Logto 不可达时，`make run` 会在 JWKS 注册阶段失败；用 `make verify`（会自动清空 issuer/audience）或手动把 `auth.issuer` / `audience` / `jwks_url` 置空，改走 X-User。
5. Nuxt 前端通过 `web/server/api/*` BFF 调 catalog/storefront；`make web-dev` 默认经 Traefik（`http://127.0.0.1:8880/catalog`、`http://127.0.0.1:8880/storefront`），直连用 `make web-dev-direct`。
6. Hatchet REST `server_url` 必须指向 Hatchet API（demo 用 `http://10.170.34.223:8088`）；`127.0.0.1:8080` 是 Kite，`/api/v1/version` 返回 v0.16.0，ScheduleOnce 会 404。gRPC DAG 不受影响。
7. pprof/MCP 不经过 Huma，已显式登记到 authz 路由目录（`systemHTTPRoutes`）；新增这类非 Huma 路由时也要登记，否则 Casbin 下 admin 也会 403。
