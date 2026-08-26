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

## app / db

| 键 | 说明 |
|----|------|
| `app.name` | 应用名（Consul 注册名、Huma 标题；`otel.service_name` 空则回退） |
| `db.driver` | `mysql` / `postgres` / `sqlite`；空 = 不连库 |
| `db.dsn` | DSN；sqlite 会补 `_busy_timeout` / WAL / `_fk`（不覆盖已有值） |
| `db.log_level` | `silent` / `error` / `warn` / `info`；默认 **warn** |
| `db.max_idle_conns` / `max_open_conns` / `conn_max_lifetime` | 池参数；lifetime 如 `1h` |

## outbox / hatchet

| 键 | 说明 |
|----|------|
| `outbox.enabled` | 事务性 outbox relay |
| `outbox.poll_interval` | 默认 `1s`；必须 > 0 |
| `outbox.batch_size` / `max_retries` / `claim_timeout` | 批大小、重试、租约 |
| `hatchet.enabled` | 启用 client（有 registrar 时启 worker） |
| `hatchet.token` / `HATCHET_CLIENT_TOKEN` | 必填（启用时；SDK 环境变量） |
| `hatchet.host_port` | 如 `localhost:7077`；loopback 默认 TLS `none` |
| `hatchet.namespace` / `worker_name` | 命名空间与 worker 名 |
| `hatchet.outbox_publisher` | `true` 时 relay 推 Hatchet events |

## auth

| 键 | 说明 |
|----|------|
| `auth.enabled` | 鉴权总开关（仅 Huma） |
| `auth.issuer` / `audience` / `jwks_url` | JWT；jwks 默认 `{issuer}/jwks` |
| `auth.org_claim` / `require_org` | 写入 `Subject.OrgID`；Enforce 不按组织隔离 |
| `auth.casbin.enabled` | path/method RBAC |
| `auth.casbin.table_name` | 默认 `casbin_rule` |
| `auth.casbin.auto_register_routes` | 启动写入策略（HTTPRoute + OpenAPI 刮取） |
| `auth.casbin.bootstrap_role` / `bootstrap_users` | 引导角色与用户 |
| `auth.casbin.reload_interval` | 多副本重载策略；默认 `5s`；`0s` 关闭 |
| `auth.dev_header_user` | 允许 `X-User`；生产必须 false |
| `auth.tls_insecure` | 跳过 JWKS TLS 校验；仅本地自签 IdP |
| `auth.deny_unregistered` | 仅 Casbin 关闭：未登记路由是否 403。默认 false 仍要登录 |

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
