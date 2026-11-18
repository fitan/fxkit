# fxkit 配置清单

常用 YAML 键（完整示例见 fxkit README）。配置树不从环境变量覆盖。

## 加载来源（高 → 低）

1. CLI `--port`
2. Consul KV：`--consul` + `--consul-key`
3. 本地 YAML `--config`
4. 各配置类型的 `SetDefaults`（`Load` / `Provide` 时）

ACL：`CONSUL_HTTP_TOKEN`（Consul 客户端惯例，不是配置树）。`--consul` 只拉配置，不回填 `discovery.consul_address`。

## server

| 键 | 说明 |
|----|------|
| `server.port` | HTTP 端口 |
| `server.log_payloads` | 记录请求/响应体（含脱敏启发式）；生产慎开 |
| `server.cors_allowed_origins` | 空 = 无 CORS（默认）；`["*"]` 才任意源 |
| `server.shutdown_timeout` | 优雅停机超时时间；默认 `10s` |
| `server.pprof.enabled` / `prefix` | 默认 false；开启后仍走 authz |
| `server.security_headers.enabled` / `hsts` / `csp` | 默认开启 nosniff/frame/referrer；HSTS/CSP 显式配置 |
| `server.csrf.enabled` / `cookie_name` / `header_name` | 仅 cookie 认证场景；double-submit |

## app / db

| 键 | 说明 |
|----|------|
| `app.name` | 应用名（Consul 注册名、Huma 标题；`otel.service_name` 空则回退） |
| `db.driver` | `mysql` / `postgres` / `sqlite`；空 = 不连库 |
| `db.dsn` | DSN；sqlite 会补 `_busy_timeout` / WAL / `_fk`（不覆盖已有值） |
| `db.log_level` | `silent` / `error` / `warn` / `info`；默认 **warn** |
| `db.slow_threshold` | 慢查询日志告警阈值；默认 `200ms`（超过则输出 WARN 慢查询日志） |
| `db.max_idle_conns` / `max_open_conns` / `conn_max_lifetime` | 池参数；lifetime 如 `1h` |

## outbox / hatchet

| 键 | 说明 |
|----|------|
| `outbox.enabled` | 事务性 outbox relay |
| `outbox.poll_interval` | 默认 `1s`；必须 > 0 |
| `outbox.batch_size` / `max_retries` / `claim_timeout` | 批大小、重试、租约 |
| `outbox.publish_concurrency` | 并行投递数，默认 `1`（按 id 串行）；未分组时失败不挡住同批后续事件；>1 不保证批内顺序 |
| `outbox.partition_field` | 按 payload 字段分组（如 `"actorId"`）。单个 relay 的同一批内同 key 串行且失败即停，不同 key 并行。跨批重试和多副本都不保证同 key 全序。空 = 不分组 |
| `outbox.purge_interval` / `purge_before` / `inbox_purge_before` | 自动清理 published/failed + inbox done；`purge_interval: 0` = 关闭；阈值默认 168h |
| `hatchet.enabled` | 启用 client（有 registrar 时启 worker） |
| `hatchet.token` / `HATCHET_CLIENT_TOKEN` | 必填（启用时；SDK 环境变量） |
| `hatchet.host_port` | 如 `localhost:7077`；loopback 默认 TLS `none` |
| `hatchet.server_url` | REST（cron/reminder）；空则用 token 里的 `server_url` |
| `hatchet.tls_strategy` | `none` / `tls` / `mtls`；空 = 仅 loopback 默认 `none` |
| `hatchet.namespace` / `worker_name` | 命名空间与 worker 名 |
| `hatchet.outbox_publisher` | `true` 时 relay 推 Hatchet events |
| `hatchet.otel` | worker `hatchet.start_step_run`；复用 otelx TracerProvider。默认 true；otelx 未开则跳过 |

## auth

| 键 | 说明 |
|----|------|
| `auth.enabled` | 鉴权总开关。中间件在 chi 上，覆盖 Huma 和裸路由 |
| `auth.issuer` / `audience` / `jwks_url` | JWT；jwks 默认 `{issuer}/jwks` |
| `auth.org_claim` / `require_org` | 写入 `Subject.OrgID`，作为 Casbin domain。空 domain 拒绝 |
| `auth.public_paths` | 匿名路径，精确或前缀。health/version/docs/spec 另有内置放行 |
| `auth.casbin.enabled` | path/method RBAC |
| `auth.casbin.table_name` | 默认 `casbin_rule` |
| `auth.casbin.auto_register_routes` | 启动写入策略（HTTPRoute + OpenAPI 刮取） |
| `auth.casbin.bootstrap_role` / `bootstrap_users` | 引导角色与用户 |
| `auth.casbin.reload_interval` | 多副本重载策略；默认 `5s`；`0s` 关闭 |
| `auth.dev_header_user` | 允许 `X-User`；生产必须 false |
| `auth.tls_insecure` | 跳过 JWKS TLS 校验；仅本地自签 IdP |
| `auth.deny_unregistered` | 已删除。写成 true 会启动失败。Casbin 关闭时登录即可；按路由拒绝请开 Casbin |

## audit

| 键 | 说明 |
|----|------|
| `audit.enabled` | 默认 false |
| `audit.sink` | `slog` / `db` / `outbox` |
| `audit.include_body` / `include_headers` | 默认 false；body 截断 8 KiB |
| `audit.redact_headers` | 默认 `Authorization` / `Cookie` / `Set-Cookie` |
| `audit.retention` | 默认 `720h` |

## idempotency

| 键 | 说明 |
|----|------|
| `idempotency.enabled` | 默认 false；只对带 `Idempotency-Key` 的 mutating 请求生效 |
| `idempotency.header` / `ttl` / `methods` / `max_body_bytes` | 默认 `Idempotency-Key` / `24h` / POST… / 1 MiB |
| `idempotency.purge_interval` | 过期 key 清理间隔；默认 `1h`，0 = 关闭 |
| `idempotency.store` | 当前只支持 `db` |

## mcp

| 键 | 说明 |
|----|------|
| `mcp.enabled` | 默认 false；开启后在 `mcp.path` 下挂 `GET /sse` + `POST /message` |
| `mcp.path` | 默认 `/mcp` |
| `mcp.include_tools` / `exclude_tools` | 按 operationId 白/黑名单；生产建议白名单 |
| `mcp.max_response_bytes` | 单次 tool 返回上限，默认 1 MiB |
| `mcp.headers` | 固定透传 header |

开启 Casbin 时，`/mcp/*` 不经过 Huma，需显式登记到 `authz.ProvideHTTPRoutes`，否则 admin 也会 403。

## otel

| 键 | 说明 |
|----|------|
| `otel.enabled` | Trace/Metric/Log |
| `otel.service_name` / `service_version` / `environment` | 资源属性 |
| `otel.endpoint` | 如 `localhost:4317` |
| `otel.protocol` | `grpc` / `http` |
| `otel.insecure` | 明文 gRPC |
| `otel.sampling` | 见 `fxkit-otel` |
| `otel.traces` / `metrics` / `logs` | 各管道 enabled/exporter |

## discovery

| 键 | 说明 |
|----|------|
| `discovery.consul_address` | 空 = 跳过注册与发现 |
| `discovery.consul_passing_only` | 查询/watch 是否仅 healthy |
| `discovery.register` | `true` 时自注册（TTL） |
| `discovery.advertise_address` | 空 = 自动本机 IP |
| `discovery.tags` | 额外 Consul 标签（与内置 `fxkit`/`http` 合并；Traefik Catalog 用） |

## config watch

| 用法 | 说明 |
|------|------|
| `config.ProvideWatch[T](key)` | opt-in 注入 `*config.Watch[T]`；只 watch Consul KV |
| `config.Dynamic[T]` | 只读接口；消费方依赖它，不依赖具体 Watch |
| `(*config.Config).Watch[T](key)` | 手动构造；`Get()` 读当前快照，`Close()` 停止 |

`ProvideConfig[T]` 仍是启动快照；框架配置（server/db/auth/otel）不建议热更新。
