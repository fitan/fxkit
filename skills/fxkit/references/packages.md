# fxkit 包 → Skill 对照

| 包 | 覆盖方式 | Skill / 说明 |
|----|----------|----------------|
| `fxkit`（Default/Minimal/Run） | 主 skill | `fxkit` |
| `cli` | 主 skill | `fxkit`（serve/openapi/version、`AddCommand`） |
| `config` | 主 skill + checklist | `fxkit` / `references/config-checklist.md` |
| `server` | 主 skill | `fxkit`（Route、PrefixRoute、ProvideMiddleware、Listener、健康检查） |
| `gormx` | 主 skill | `fxkit` |
| `fxerrors` | 主 skill | `fxkit`（构造表） |
| `logx` | 主 skill（附） | 控制台 slog；otel 启用时由 otelx 接管 fanout |
| `buildinfo` | 主 skill（附） | `/version`、`-ldflags`；Consul 元数据补丁会用到 |
| `cmd/fxkit` | 主 skill CLI 节 | `fxkit init` / `new` / `gen resource` / `gen client` / `gen mcp` |
| `huma` | 专用 | `fxkit-huma-crud`（含 ProvideMiddleware） |
| `crudx` | 专用 | `fxkit-huma-crud`（`List` + `ListSpec`；`Repo` 可选） |
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

## 有意不单独建 skill

| 主题 | 原因 |
|------|------|
| Dapr | 默认栈已移除；异步用 Hatchet |
| Atlas migrate / 前端 | 属宿主或其它仓库 skill，不在 fxkit 包内 |

查缺时：每个 `fxkit/<pkg>` 应出现在上表；若新增子包，补一行并决定并入现有 skill 还是新建。
