# 生产上线清单（fxkit）

> 适用于「内部平台服务」定位：PostgreSQL / MySQL + OTel，可选 Consul / Hatchet。
> 对外公网 API 还需网关限流、渗透测试与认证边界审计。

---

## 0. 前置条件

- [ ] **锁定 fxkit 版本**（`go.mod` pin，或 vendor）。框架仍在演进，升级请当作代码变更。
- [ ] 确认 Go 版本与 `go.mod` 一致（当前要求 1.27+）。
- [ ] 确认 SQL 上线平台可用，且能执行 `myapp migrate print` 产出的 DDL。

---

## 1. 配置收敛（逐项确认）

```yaml
app:
  name: "<service-name>"          # Consul 注册名 / OTel service.name 回退

server:
  port: "8080"
  log_payloads: false             # 生产必须 false
  access_log: true                # 建议开启（route 用模板，不高基数）
  cors_allowed_origins: ["https://your-console.example.com"]   # 不要用 "*"
  rate_limit:
    enabled: true                 # 网关限流之外再加一层
    rps: 50
    burst: 100

db:
  driver: "postgres"              # 或 mysql
  # 必须带语句级超时（见下方说明）：
  #   postgres: ...?sslmode=disable&statement_timeout=30000      (毫秒，pgx 作为 runtime param 下发)
  #   mysql:    ...?parseTime=true&readTimeout=30s&writeTimeout=30s
  dsn: "<from secret manager>"
  log_level: "warn"
  max_open_conns: 100             # 按实例数 × DB 上限规划
  auto_migrate: false             # 生产必须 false
  check_schema: true              # 发布顺序错 → 启动失败（好事）

auth:
  enabled: true
  issuer: "<oidc issuer>"
  audience: "<api resource>"
  dev_header_user: false          # 生产必须 false
  deny_unregistered: false        # 或按需 true
  # mux_middleware: true          # 需要覆盖裸 chi / docs / mcp 时开启
  public_paths: ["/public"]       # 只留真正匿名端点

docs:
  enabled: false                  # 生产默认关闭
mcp:
  enabled: false                  # 生产默认关闭

otel:
  enabled: true
  endpoint: "<collector>:4317"
  protocol: "grpc"
  insecure: false
  sampling: "trace_id_ratio:0.1"
```

- [ ] `auth.dev_header_user=false`
- [ ] `docs.enabled=false`、`mcp.enabled=false`
- [ ] `db.auto_migrate=false`、`db.check_schema=true`
- [ ] `db.dsn` 已带语句级超时（`statement_timeout` / `readTimeout`）
- [ ] `server.log_payloads=false`
- [ ] CORS 不是 `*`
- [ ] 限流已开（边缘靠网关；框架内限流在网关后应关闭，否则退化成整站单桶）

### 为什么 DSN 必须带语句级超时

gorm 不会自动设 `statement_timeout`，框架也不设。一条卡住的 SQL（锁等待、缺索引的全表扫、
错误的 JOIN）会**一直占着一条连接直到它自己返回**；连接池默认 `max_open_conns: 100`，
几条并发跑飞就可能把池占满，表现为「所有接口一起变慢/超时」。

HTTP 层超时解决不了这个问题——它只能让客户端早点收到 504，被占住的连接依然在那里。
管住 SQL 本身才是根治：

| 方言 | 参数 | 含义 |
|---|---|---|
| PostgreSQL | `statement_timeout=30000` | 单条语句最长 30s，超时由服务端中止（单位毫秒） |
| MySQL | `readTimeout=30s` / `writeTimeout=30s` | 网络读写超时，卡住的查询会因读超时而失败 |

> 注意：`statement_timeout` 只约束**服务端执行**，不含网络往返；`max_execution_time`（MySQL）
> 只对只读语句生效。按你的实际 P99 取 3–5 倍，别一刀切设得比正常慢查询还小。

---

## 2. 数据库变更顺序（关键）

```
① 生成 DDL       myapp migrate print --dialect postgres > /tmp/fxkit-0001.sql
② 平台审批执行    ← 必须在下发代码之前完成
③ 校验契约        myapp migrate check -c configs/config.yaml   → schema OK
④ 滚动发布        新副本启动时 check_schema 会再校验一次
```

- 框架只拥有 4 张表：`outbox_events`、`outbox_inbox_events`、`casbin_rule`、`authz_role`。
- 业务表由业务迁移管理，建议与框架表使用**不同的版本表**。
- 回滚脚本：`gormx/migrations/*/0001_init.rollback.sql`（仅开发/预发使用；生产回滚优先前向修复）。

---

## 3. 灰度与回滚

| 步骤 | 观察项 | 时长 |
|---|---|---|
| 1 副本 | 错误率、P99、`server.readyz.checks{status=error}`、`outbox.event.lag` | 30 min |
| 10% | 同上 + `consulx.lock.lost`（多副本时） | 30 min |
| 50% → 100% | 同上 | 持续 |

**回滚**：镜像回滚即可。`0001_init` 只新增表，无需回退 DDL。
若已启用 Hatchet 且创建了 cron，回滚前先 `DeleteReminder` 清理。

---

## 4. 告警（PromQL 示例）

> OTel 指标名里的 `.` 导出后通常变成 `_`；下面按常见转换书写，落地时以你的 collector 为准。

```promql
# 缓存后端故障
sum(rate(cachex_lookups_total{cache_result="error"}[5m])) > 0.1

# outbox 积压（P99 入队→发布延迟 > 60s）
histogram_quantile(0.99, sum(rate(outbox_event_lag_bucket[5m])) by (le)) > 60

# 鉴权判定异常
sum(rate(authz_enforce_results_total{authz_result="error"}[5m])) > 0

# 列表查询错误突增
sum(rate(crudx_list_errors_total[5m])) > 1

# 分布式锁丢失（必须为 0）
increase(consulx_lock_lost_total[10m]) > 0

# 就绪探针失败
sum(rate(server_readyz_checks_total{status="error"}[5m])) > 0

# MCP tool 失败
sum(rate(mcpx_tool_calls_total{mcp_result="error"}[5m])) > 0

# 启动变慢
histogram_quantile(0.95, sum(rate(fxkit_startup_duration_bucket{phase="start"}[15m])) by (le)) > 30
```

---

## 5. 南北向网关

公网入口必须经过网关，完整方案见 **[gateway-traefik.md](./gateway-traefik.md)**。

- [ ] TLS 终止（网关或前置 LB），HTTP 全部跳转 HTTPS
- [ ] 安全响应头（HSTS / nosniff / referrer-policy）
- [ ] 边缘限流（按 API Key 或真实 IP，**不是**按网关 IP）
- [ ] 熔断（异常实例快速摘除，避免重试放大）
- [ ] `maxRequestBodyBytes` 与 `server.max_body_bytes` 对齐
- [ ] SSE/MCP 路由：不挂 buffering，entryPoint `writeTimeout: 0`
- [ ] `/docs`、`/openapi.*`、`/mcp`、`/authz/*` 无公网路由
- [ ] 超时链路：网关 ≥ `server.write_timeout` ≥ 业务耗时
- [ ] `server.rate_limit.enabled=false`（网关后它失去按客户端限流的意义）

---

## 6. 多副本注意事项

- [ ] Consul 锁：确认 `TTL`（默认 15s）大于一次续租 RTT 的 3 倍以上；关注
      `consulx.lock.renew.failures` 与 `consulx.lock.lost`。
- [ ] outbox relay：多副本会各自 claim，`publish_concurrency` 默认 1；确认 `claim_timeout` > 单批处理时间。
      actor 事件需要高吞吐时用 `partition_field: "actorId"`（同 actor 保序、不同 actor 并行），
      不要仅调大 `publish_concurrency`，否则同一 actor 的命令可能乱序执行。
- [ ] Hatchet worker：`worker_slots` 与实例数匹配；`hatchetx.actor.runs` 用于观察邮箱积压。
- [ ] 优雅停机：`stop-timeout` 要大于最慢请求；LB 摘流要在 SIGTERM 之前完成。

---

## 7. 安全清单

- [ ] JWT：`issuer` / `audience` / JWKS 可达；`tls_insecure=false`
- [ ] Casbin：`bootstrap_role` 不授予通配；`auto_bind_bootstrap=false`
- [ ] `q=` 白名单：`Likeable` 只给确实需要模糊搜索的列
- [ ] 管理 API（`/authz/*`）只对内网/管理平台开放
- [ ] 密钥（DB/Redis/Hatchet token）走 secret manager，不入库不入日志
- [ ] 访问日志已脱敏（框架默认不记 body；`log_payloads=false`）

---

## 8. 上线后 24h 观察

- [ ] `/readyz` 无抖动
- [ ] `outbox.event.lag` P99 稳定
- [ ] `crudx.list.duration` P99 在预算内
- [ ] 无 `consulx.lock.lost`
- [ ] 无 `authz.enforce{result=error}`
- [ ] 日志无 `unknown column` / `relation does not exist`（说明 DDL 顺序错）
