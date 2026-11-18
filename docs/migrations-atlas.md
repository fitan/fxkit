# 用 Atlas 管理 fxkit 迁移

> 目标：框架不执行 DDL，但提供**可执行的迁移基线**；Atlas 负责版本化、lint、锁和 apply。

## 1. 职责边界

| 角色 | 负责 |
|---|---|
| fxkit | 框架表 DDL（`migrate print`）、`migrate baseline`、启动期 `migrate check` |
| Atlas | 版本表、迁移锁、`migrate lint` / `migrate apply`、schema 漂移检查 |
| 应用 | 业务表迁移、发布流程、备份/PITR |

框架**不**把 Atlas 作为 Go 依赖，避免 `go.mod` 膨胀；Atlas 作为 CI/CD 的外部工具。

## 2. 首次接入

```bash
# 1) 安装 atlas CLI（见 https://atlasgo.io）
# 2) 生成框架基线
./bin/catalog migrate baseline --dialect postgres --dir migrations/postgres

# 3) 配置 DATABASE_URL 与 dev 数据库
export DATABASE_URL='postgres://user:pass@localhost:5432/app?sslmode=disable'

# 4) 查看状态
atlas migrate status --env local
```

`migrations/postgres/0001_fxkit_core.sql` 包含框架表：

- `outbox_events` / `outbox_inbox_events`
- `idempotency_keys`
- `ledger_accounts` / `ledger_transactions` / `ledger_postings`
- `audit_events`
- `casbin_rule` / `authz_role`

已有数据库接入：

```bash
# 以现有库为准生成基线（必要时人工整理）
atlas schema inspect --url "$DATABASE_URL" > schema.hcl
```

## 3. 日常变更

```bash
# 业务 schema 用 HCL 或 SQL 描述；生成版本化迁移
atlas migrate diff add_orders \
  --dir file://migrations/postgres \
  --to file://schema.hcl \
  --dev-url "docker://postgres/16/dev"

# CI：拦截破坏性变更
atlas migrate lint \
  --dir file://migrations/postgres \
  --dev-url "docker://postgres/16/dev" \
  --latest 1

# 发布：独立 Job 执行
atlas migrate apply \
  --dir file://migrations/postgres \
  --url "$DATABASE_URL"
```

## 4. 运行时

- 应用启动**不执行 DDL**；
- `db.auto_migrate: false`；
- `db.check_schema: true` 或显式运行 `migrate check`，在代码先上、DDL 未执行时 fail fast。

## 5. 发布顺序

```text
1. 备份 / 确认 PITR
2. atlas migrate lint（CI）
3. atlas migrate apply（单 Job）
4. 发布应用
5. 验证 / 监控
```

多副本不要各自执行迁移；Atlas 的迁移锁只能防止并发执行，不能替代发布编排。

## 6. 回滚

Atlas 不会自动生成 down。破坏性变更要求：

- 先备份；
- 分两步发布（先加列/双写，再删旧列）；
- 回滚以恢复备份/前向修复为主，不依赖自动 down。

## 7. 方言

- `migrations/postgres`、`migrations/mysql`、`migrations/sqlite` 分开维护；
- SQLite 只用于本地/测试；
- 生产至少覆盖 Postgres 或 MySQL 其中一种。
