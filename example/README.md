# fxkit showcase（example）

独立 Go module（`replace github.com/fitan/fxkit => ../`）。用 **catalog + storefront** 把 `fxkit.Default()` 里的包都跑一遍（`docs` 换成 `docs.MergedModule`，否则 `/docs` 仍是占位骨架）。

```
Browser → Traefik :8880
            ├─ /  /callback        静态页（Logto SPA）
            ├─ /catalog/…          StripPrefix → catalog（Consul LB）
            └─ /storefront/…       StripPrefix → storefront → reqx → catalog
```

## 覆盖对照

| 包 | 例子里怎么测 |
|----|----------------|
| `fxkit` Default / Service / ProvideConfig / Routes | catalog `article` + `comment` + `platform`；storefront `digest` |
| `cli` serve / openapi / version / AddCommand | `catalog doctor`；`make openapi SVC=catalog` |
| `config` YAML + Consul KV + `--port` | replica 2：`--consul` + `--consul-key`；`--port 8083` |
| `server` chi Route / PrefixRoute / ProvideMiddleware / CORS / health | `/chi/*`、`X-Fxkit-Chi`、CORS OPTIONS、`/healthz` `/readyz` |
| `fxerrors` | chi `/chi/errors/{kind}`；Huma duplicate title 409 |
| `gormx` | Postgres + `Transaction` + AutoMigrate |
| `buildinfo` | `/version` JSON（Makefile ldflags） |
| `logx` / `otelx` | slog + OTLP HTTP `10.170.34.223:4318`；Hatchet worker 挂 `hatchet.start_step_run`（`hatchet.otel`） |
| `huma` RegisterResource + Register + ProvideMiddleware | articles 五件套；comments 单操作；`X-Fxkit-Huma` |
| `crudx` | q=`~` / `or()` / `.isnull` / JOIN / cursor / MapDBError / GetByID |
| `authz` | 未登录 401、JWT（可选）、alice admin、bob reader 403、charlie 无角色、`/authz/*`、`authz.public`、`SubjectFromContext` |
| `hatchetx` + `outbox` | 事务 outbox → `article-created` → inbox.Once；`NewActor` 计数；`CreateReminder` heartbeat |
| `consulx` | TTL 注册 + Traefik tags |
| `reqx` | storefront `NewFromFactory`（Consul watch + failover + OTel） |
| `docs` / `openapi` | MergedModule `/openapi.yaml`；Go `fxkit gen client`；前端 `openapi-typescript` + `openapi-fetch`（`make gen-web`） |
| `cmd/fxkit` | `make gen-client` |

`fxkit.Minimal()` 不单独起服务：它是 Default 的子集，本例走完整栈。

## 运行

需要本机 Go 1.26+ 与 Docker。拉依赖走 `GOPROXY=https://goproxy.cn,direct`。

```bash
cd example
make tidy && make build
make edge                 # Traefik 3.7.12
make verify               # 起 2×catalog + storefront，打 Traefik；结束后服务继续跑
make down
```

浏览器：<http://127.0.0.1:8880>（未登录走 `X-User: alice`；点 **Logto 登录** 后走 Bearer）。Dashboard：<http://127.0.0.1:8881>。前端源码在 `web/src`，改 OpenAPI 后 `make gen-web` 重生类型并打包 `web/app.js`。

本地身份：`X-User: alice`（Casbin bootstrap admin）、`bob`（reader，只能 GET 文章/评论）、`charlie`（无角色 → 403）。生产请关 `auth.dev_header_user`。

## Logto 控制台（必配）

文档里的 `http://localhost:3000` **不要用**。前端经 Traefik 跑在 **`:8880`**。应用已是 SPA，`appId` = `2h3jba3gurf7r3vsvqydb`，endpoint = `https://10.170.34.223:3001/`。

打开 Logto Console，对这个应用填：

| 项 | 值（必须完全一致） |
|----|-------------------|
| 重定向 URI | `http://127.0.0.1:8880/callback` 和 `http://localhost:8880/callback` |
| 退出登录后重定向 URI | `http://127.0.0.1:8880/` 和 `http://localhost:8880/` |
| CORS allowed origins | 一般会跟 redirect 同源自动放行；若 token 交换报 CORS，再显式加 `http://127.0.0.1:8880`、`http://localhost:8880` |

再用局域网 IP 打开页面时（例如 `http://10.170.34.223:8880`），把对应 origin 的 `/callback` 和 `/` 也加进去。

**API Resource（拿不到 Bearer 就 401/403）：**

1. API resources → 创建，**Identifier** 必须是 `https://fxkit.showcase/api`（与 catalog/storefront 的 `auth.audience` 一致）。
2. 回到该 SPA → API resources → 勾选这个 API。
3. 可选：设为 Default API resource，避免拿到 opaque token。

**浏览器信任自签证书：** 先用**同一个浏览器**打开 <https://10.170.34.223:3001/>，点继续访问。否则 SDK 拉 OIDC discovery / token 会被证书拦住。

登录后 JWT 的 `sub` 是 Logto 用户 id，不是 `alice`。第一次调 `/catalog/articles` 会 **403**。点页面上的 **把 JWT sub 绑到 admin**（用 `X-User: alice` 调 `PUT /catalog/authz/subjects/{sub}/roles`），或：

```bash
SUB='<页面上显示的 sub>'
curl -X PUT -H 'X-User: alice' -H 'Content-Type: application/json' \
  -d '{"roles":["admin"]}' \
  "http://127.0.0.1:8880/catalog/authz/subjects/${SUB}/roles"
```

改过 Traefik 本地插件后需要重启入口：`cd example && docker compose up -d --force-recreate traefik`。

Hatchet：token 放 `example/tmp/hatchet.token`（gitignore，对应本机 `localhost:7077`）。`make verify` 会自动读这个文件。

```bash
# 或
export HATCHET_CLIENT_TOKEN='...'
```

引擎 gRPC 是 `10.170.34.223:7077`（token 里常写 localhost，那是 lite 默认声明，连远程时仍用 `hatchet.host_port`）。明文：`hatchet.tls_strategy: none`。Logto 自签：`auth.tls_insecure: true`。OTLP **4318** / `otel.protocol: http`。

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
curl http://127.0.0.1:8081/chi/echo?msg=hi      # 裸 chi，不经 authz
curl -H 'X-User: alice' http://127.0.0.1:8081/me
curl -H 'X-User: bob' http://127.0.0.1:8081/articles
curl -H 'X-User: alice' http://127.0.0.1:8081/authz/roles
curl -H 'X-User: alice' http://127.0.0.1:8081/openapi.yaml | head
curl -H 'X-User: alice' http://127.0.0.1:8082/digest
```

更新 typed 客户端（storefront 调 catalog）：

```bash
make openapi SVC=catalog   # 需要可达的 Postgres；hatchet.enabled 时还要 token
make gen-client
```

## 已知缺口

1. **Hatchet token** 未提供且 `tmp/hatchet.token` 不存在时，`make verify` 会关掉 hatchet/outbox；actor / reminder / indexed 会 SKIP。
2. **`fxkit.Default()` 的 `/docs` 是占位**。本例用 `docs.MergedModule` 换成 live spec。
3. Consul 用本机 `http://localhost:8500`。第二副本用 Consul KV 拉同一份 YAML。
