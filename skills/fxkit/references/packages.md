# fxkit 包 → Skill 对照

| 包 | 覆盖方式 | Skill / 说明 |
|----|----------|----------------|
| `fxkit`（Default/Minimal/Run） | 主 skill | `fxkit` |
| `cli` | 主 skill | `fxkit`（serve/openapi/version、`AddCommand`） |
| `config` | 主 skill + checklist | `fxkit` / `references/config-checklist.md` |
| `server` | 主 skill | `fxkit`（Route、PrefixRoute、ProvideMiddleware、Listener、健康检查） |
| `cachex` | 主 skill | `fxkit`（仅 Redis 或禁用；Singleflight、TTL 抖动、负缓存） |
| `gormx` | 主 skill | `fxkit` |
| `fxerrors` | 主 skill | `fxkit`（构造表） |
| `audit` | 专用（opt-in） | `fxkit`（审计日志：slog/db/outbox sink） |
| `idempotency` | 专用（opt-in） | `fxkit`（HTTP Idempotency-Key，key 按 method/path/subject 作用域） |
| `money` | 专用 | `fxkit-payments`（最小货币单位） |
| `ledger` | 专用 | `fxkit-payments`（复式记账） |
| `payment` | 专用 | `fxkit-payments`（状态机 + CAS） |
| `webhook` | 专用 | `fxkit-payments`（验签） |
| `reconciliation` | 专用 | `fxkit-payments`（对账） |
| `logx` | 主 skill（附） | 控制台 slog；otel 启用时由 otelx 接管 fanout |
| `buildinfo` | 主 skill（附） | `/version`、`-ldflags`；Consul 元数据补丁会用到 |
| `cmd/fxkit` | 主 skill CLI 节 | `fxkit init` / `new` / `gen resource` / `gen client` |
| `huma` | 专用 | `fxkit-huma-crud`（含 ProvideMiddleware） |
| `crudx` | 专用 | `fxkit-huma-crud`（`List` + `ListSpec`） |
| `hatchetx` | 专用 | `fxkit-hatchet` |
| `outbox` | 专用 | `fxkit-hatchet` |
| `authz` | 专用 | `fxkit-authz` |
| `otelx` | 专用 | `fxkit-otel` |
| `consulx` | 专用 | `fxkit-discovery` |
| `reqx` | 专用 | `fxkit-reqx` |
| `docs` | 专用 | `fxkit-docs` |
| `openapi` | 专用 | `fxkit-docs` |
| `mcpx` | 专用 | `fxkit-mcp`（OpenAPI/Huma -> LLM Tool Calling & MCP Server） |
| `skills/` | 本目录 | 安装说明见 `../README.md` |
| （工程结构，非包） | 专用 | `fxkit-layout`（`services/`、`entity`、`repository`、feature 包、命名与依赖方向） |

## 有意不单独建 skill

| 主题 | 原因 |
|------|------|
| Dapr | 默认栈已移除；异步用 Hatchet |
| Atlas migrate / 前端 | 属宿主或其它仓库 skill，不在 fxkit 包内 |

查缺时：每个 `fxkit/<pkg>` 应出现在上表；若新增子包，补一行并决定并入现有 skill 还是新建。

> 框架自身维护 skill 在 [`../../../.agents/skills/fxkit-maintain/SKILL.md`](../../../.agents/skills/fxkit-maintain/SKILL.md)，不属于消费方 skill，不随 `cp -R skills/fxkit*` 复制。
