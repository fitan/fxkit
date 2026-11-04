# 测试指南

本文汇总 fxkit 的测试分层与「怎么在本地跑真实依赖的集成测试」。

---

## 1. 单元测试（无外部依赖）

```bash
go test ./...                 # 全部
go test -race ./...           # 并发相关，CI 必跑
FXKIT_STRICT_WIRING=1 go test ./...   # 装配期读 group 直接 panic
```

`FXKIT_STRICT_WIRING=1` 会把「模块 Option 里读 `di.GetGroup`」变成 panic。
这类写法以前表现为「启动成功但路由/worker 不存在」，很难排查。

---

## 2. 集成测试（env 门控）

这些测试**默认 skip**，只有设置对应环境变量才会连接真实依赖。

| 测试 | 环境变量 | 依赖 |
|---|---|---|
| `crudx.TestDialect*` | `FXKIT_TEST_POSTGRES_DSN` / `FXKIT_TEST_MYSQL_DSN` | PostgreSQL / MySQL |
| `gormx.TestMigrationsPostgres` | `FXKIT_TEST_POSTGRES_DSN` | PostgreSQL |
| `gormx.TestMigrationsMySQL` | `FXKIT_TEST_MYSQL_DSN` | MySQL |
| `cachex.TestRedisIntegration*` | `FXKIT_TEST_REDIS_ADDR` | Redis |
| `consulx.TestConsul*Integration` | `FXKIT_TEST_CONSUL_ADDR` | Consul（dev agent） |
| `consulx.TestConsulLockFailureModes` | `FXKIT_TEST_CONSUL_ADDR` | Consul |
| `reqx.TestDiscoveryIntegration` | `FXKIT_TEST_CONSUL_ADDR` | Consul |
| `hatchetx.TestWorkerIntegration` 等 | `FXKIT_TEST_HATCHET_TOKEN`（或 `_TOKEN_FILE`） | Hatchet 引擎 |
| `example/scripts/verify-local.sh` | 无（SQLite） | 无 |

### 2.1 一键起依赖（Docker）

```bash
docker run -d --name fxkit-pg     -e POSTGRES_USER=fxkit -e POSTGRES_PASSWORD=fxkit \
  -e POSTGRES_DB=fxkit -p 55432:5432 postgres:16
docker run -d --name fxkit-mysql  -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=fxkit \
  -p 53306:3306 mysql:8.0
docker run -d --name fxkit-redis  -p 56379:6379 redis:6
docker run -d --name fxkit-consul -p 58500:8500 hashicorp/consul:1.21 agent -dev -client=0.0.0.0
```

### 2.2 跑全部集成测试

```bash
FXKIT_TEST_POSTGRES_DSN="postgres://fxkit:fxkit@127.0.0.1:55432/fxkit?sslmode=disable" \
FXKIT_TEST_MYSQL_DSN="root:root@tcp(127.0.0.1:53306)/fxkit?parseTime=true&multiStatements=true" \
FXKIT_TEST_REDIS_ADDR=127.0.0.1:56379 \
FXKIT_TEST_CONSUL_ADDR=http://127.0.0.1:58500 \
  go test -count=1 ./...
```

### 2.3 Hatchet（真实引擎）

```bash
# 在 Hatchet 部署上生成短时效 tenant token
docker compose run --rm setup-config /hatchet/hatchet-admin token create \
  --config /hatchet/config --tenant-id <tenant-id> --name fxkit-itest --expiresIn 2h

FXKIT_TEST_HATCHET_TOKEN_FILE=/tmp/hatchet-token.txt \
FXKIT_TEST_HATCHET_HOST_PORT=10.170.34.223:7077 \
FXKIT_TEST_HATCHET_SERVER_URL=http://10.170.34.223:8088 \
FXKIT_TEST_HATCHET_TLS_STRATEGY=none \
  go test -count=1 -run 'TestWorkerIntegration|TestActor|TestReminder|TestOutboxToHatchet' -v ./hatchetx
```

覆盖：worker 端到端、Actor 邮箱串行、跨 actorId 并行、Cron 真正触发、Reminder CRUD、outbox→Hatchet。
副作用：临时 workflow / run / cron，测试结束会尽力清理。

> 自建 Hatchet 用 Postgres 队列时，引擎侧需 `SERVER_MSGQUEUE_KIND=postgres`（PG 需 `pgmq` 扩展）。

---

## 3. 端到端（HTTP）

```bash
cd example && bash scripts/verify-local.sh
```

SQLite 单机、无需外部依赖，覆盖 24 项：健康检查、鉴权矩阵（401/403/200）、
游标翻页（desc 第二页）、`q=` 过滤、非法排序、`/docs` live spec、MCP 默认关闭、authz 管理 API。

---

## 4. 基准测试

```bash
go test -run XXX -bench . -benchmem ./authz ./logx ./cachex ./crudx
```

热路径基线（参考）：路由匹配约 46ns/0 alloc，控制台日志 0 alloc。

---

## 5. CI 对应关系

| 本地命令 | CI job |
|---|---|
| `go test -race ./...` + `FXKIT_STRICT_WIRING=1` | `test` |
| PG/MySQL 集成测试 | `dialect` |
| Redis + Consul 集成测试 | `integration` |
| `atlas migrate lint` | `atlas` |
| `example` 构建/测试 | `example` |
| `verify-local.sh` | `smoke` |
| `fxkit new` / `gen resource` 编译 | `generated-code` |
| `--di-graph` + `validate` | `di-graph` |

---

## 6. 常见问题

| 现象 | 原因 / 处理 |
|---|---|
| `httptest: failed to listen ... operation not permitted` | 沙箱禁止 bind；集成测试需要非沙箱环境 |
| `curl` 访问 `127.0.0.1` 返回 503 | 本机设置了 `http_proxy`；加 `NO_PROXY=127.0.0.1` 或 `curl --noproxy '*'` |
| 集成测试全 skip | 忘记设环境变量（用 `-v` 能看到 skip 原因） |
| `go test` 卡住/写缓存失败 | 沙箱限制 `GOCACHE`；用可写目录或非沙箱运行 |
| Hatchet 测试 `401/403` | token 过期或 tenant 不匹配；重新生成 token |
| `TestDialect/mysql` 报 `multiStatements` | DSN 需要 `multiStatements=true` |
