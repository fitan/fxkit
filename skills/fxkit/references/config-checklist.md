# fxkit 配置清单

常用 YAML 键（完整示例见 fxkit README）。环境变量：`FXKIT_` + 路径，`.` → `_`。

## 加载来源（高 → 低）

1. CLI `--port`
2. `FXKIT_*` 环境变量
3. Consul KV：`--consul` + `--consul-key`（或 `FXKIT_CONFIG_CONSUL` / `FXKIT_CONFIG_CONSUL_KEY`）
4. 本地 YAML `--config`
5. `config/defaults.go`

ACL：`CONSUL_HTTP_TOKEN`。

## server

| 键 | 说明 |
|----|------|
| `server.port` | HTTP 端口 |
| `server.log_payloads` | 记录请求/响应体（含脱敏启发式）；生产慎开 |
| `server.cors_allowed_origins` | 空 = 无 CORS；`["*"]` 才任意源 |

## app / db

| 键 | 说明 |
|----|------|
| `app.name` | 应用名（Consul 注册名） |
| `db.driver` | `mysql` / `postgres` / `sqlite`；空 = 不连库 |
| `db.dsn` | DSN |
| `db.log_level` | `silent` / `error` / `warn` / `info` |
| `db.max_idle_conns` / `max_open_conns` / `conn_max_lifetime_sec` | 池参数 |

## outbox / hatchet

| 键 | 说明 |
|----|------|
| `outbox.enabled` | 事务性 outbox relay |
| `outbox.poll_interval` | 默认 `1s`（≤0 回退 1s） |
| `outbox.batch_size` / `max_retries` / `claim_timeout` | 批大小、重试、租约 |
| `hatchet.enabled` | 启用 client（有 registrar 时启 worker） |
| `hatchet.token` / `HATCHET_CLIENT_TOKEN` | 必填（启用时） |
| `hatchet.host_port` | 如 `localhost:7077`；loopback 默认 TLS `none` |
| `hatchet.namespace` / `worker_name` | 命名空间与 worker 名 |
| `hatchet.outbox_publisher` | `true` 时 relay 推 Hatchet events |

## auth

| 键 | 说明 |
|----|------|
| `auth.enabled` | 鉴权总开关（仅 Huma） |
| `auth.issuer` / `audience` / `jwks_url` | JWT；jwks 默认 `{issuer}/jwks` |
| `auth.org_claim` / `require_org` | 组织声明 |
| `auth.casbin.enabled` | path/method RBAC |
| `auth.casbin.table_name` | 默认 `casbin_rule` |
| `auth.casbin.auto_register_routes` | 启动写入策略 |
| `auth.casbin.bootstrap_role` / `bootstrap_users` | 引导角色与用户 |
| `auth.dev_header_user` | 允许 `X-User`；生产必须 false |
| `auth.deny_unregistered` | 未登记路由是否 403 |

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
| `discovery.advertise_address` | 空 = 自动本机 IP；或 `FXKIT_ADVERTISE_ADDRESS` |
