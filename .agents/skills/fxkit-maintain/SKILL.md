---
name: fxkit-maintain
description: >-
  Maintain and continuously improve the fxkit framework itself (not consumer services):
  audit for drift, choose minimal high-value changes, avoid over-engineering and
  over-defensiveness, verify, and keep docs/usage skills in sync. Use when asked to
  optimize/refactor/review fxkit, when docs or skills may be stale, when changing
  public APIs/config/CLI, or when running a framework audit.
---

# fxkit 框架维护

适用范围：修改 **fxkit 框架本身**（`di/`、`server/`、`crudx/`、`outbox/`、`hatchetx/` 等），不是用 fxkit 写业务服务。

- 消费方使用说明：`skills/`（`fxkit`、`fxkit-huma-crud`、`fxkit-hatchet` …）
- 本 skill：框架内部演进、重构、评审、持续优化
- 项目文档：`docs/guide.md`、`README.md`、各包 `README.md`

## 两条硬原则

### 1. 不过度设计

- **没有第二个真实调用方，不抽接口**。一个实现 + 一个 interface = 噪音；只有测试 fake、多实现、外部边界才值得抽。
- **不为“将来可能”加扩展点**。先写具体实现；出现第二个用例再抽象。
- **不为每个常量加配置**。配置键必须有明确使用场景、默认值和 Validate。
- **不造通用层**：`BaseService`、`BaseRepo`、`Controller`、泛型 CRUD 包装器，除非它们解决的是已经重复出现的问题。
- **单一事实来源**：默认值只在 `SetDefaults`，校验只在 `Validate`；不要在 loader、provider、handler 各写一遍。
- **编译期优先**：不用反射、动态注册、隐式扫描；`di` 已经因为反射付出过代价。
- **依赖最小化**：stdlib + 已有依赖；新依赖要说明不可替代性。
- **最小 diff**：优先改现有函数/类型，而不是新建包或导出新符号。

### 2. 不过度防御

- **只在系统边界校验**：HTTP 入参、DB 写入、外部服务响应、配置加载。
- **内部信任调用方**：不要在框架内部每个函数开头 nil-check 所有参数。
- **错误只 wrap 一次**：不要在 service/repo/handler 层层包成日志噪音。
- **配置错误启动失败**：`Validate` 拒绝非法值，不静默回退到“看起来能用”的默认值。
- **不为不可能的状态写分支**：直接返回错误或 panic 比兜底更诚实。
- **不无测量地加锁/重试/缓存**：先有并发或性能证据。
- **不吞错**：要么返回，要么记录并返回。

### 边界判断

| 位置 | 该做什么 |
|---|---|
| HTTP handler / Huma | 校验用户输入，返回 `fxerrors` |
| service / use case | 业务规则、事务边界、outbox |
| repository / DB | 约束、`MapDBError` |
| 外部 client | 超时、重试、状态码映射 |
| 框架内部函数 | 信任调用方，保持简单 |

## 优化循环（每次优化都按这个走）

1. **观察**：先读代码、文档、skill、测试，不先下结论。
2. **证据**：问题要落到 `file:line`、失败测试、benchmark、或文档与代码的 diff。
3. **最小改动**：优先改现有函数/类型；一次只解决一个问题。
4. **验证**：跑对应测试 + race + example + 文档链接检查。
5. **同步**：文档、使用 skill、配置清单、包索引一起更新。
6. **沉淀**：可复用结论写回本 skill。

没有证据的“感觉可以优化”不做。

## 发现优化机会（审计清单）

每次做框架审计时，按下面扫。发现的问题按优先级处理，不要一次全改。

### 代码与测试

- `go list ./...` / `go vet ./...` / `gofmt -l .`
- `make test` / `make test-race` / `make ci`
- `cd example && go build ./... && go test ./...`
- 搜 `TODO` / `FIXME` / `deprecated` / `panic(`，确认是否该处理。
- 重复的默认值、校验、错误映射、配置解析。
- 单实现接口、泛型包装器、god package。
- 失败路径/并发路径是否有测试。

**测试 fake 必须贴合真实协议**：fake 返回了真实服务不会返回的 header/字段，会让 bug 在测试里消失。例：Consul 的 `PUT /v1/kv/...?acquire=` 不返回 `X-Consul-Index`，只有 GET/blocking query 返回；fake 之前返回了，导致 `FencingToken` 恒为 0 的问题长期未被发现。修 fake 时优先让它模拟真实行为的“缺失”。

### 文档与 skill 漂移

- 配置键：`grep -R 'yaml:"' <pkg>` vs `skills/fxkit/references/config-checklist.md`。
- 公开 API：`go doc` vs 对应 `skills/fxkit-*/SKILL.md`。
- Bundle：`fxkit.go` vs `docs/guide.md` §2.1 + `skills/fxkit/SKILL.md`。
- CLI：`cli/cli.go` vs `skills/fxkit/SKILL.md` + `docs/guide.md`。
- 脚手架：`cmd/fxkit/templates` vs `skills/fxkit-layout` / `fxkit-docs`。
- 包列表：目录 vs `skills/fxkit/references/packages.md`。

### 示例

- `example` 是否还能编译、测试、`make check`。
- 示例是否用了已删除/改名的 API。
- 示例目录结构是否与 `fxkit-layout` 一致。
- **每个 opt-in 能力是否都有可运行用例**：只写模块/配置但接口 503 不算覆盖。示例应能回答“这个能力怎么用、失败时长什么样”。
- 非 Huma 路由（pprof / `/mcp/*` / 裸 chi）是否登记进 `authz.ProvideHTTPRoutes`；否则 Casbin 下 admin 也 403。

## 优先级

| 级别 | 类型 | 处理 |
|---|---|---|
| P0 | 正确性、安全、数据丢失、并发 bug | 立即修 + 回归测试 |
| P1 | 公开 API 不一致、配置静默失效、迁移风险 | 先提案，确认后修 |
| P2 | 文档/skill 漂移、重复事实来源、示例失效 | 可直接修 |
| P3 | 性能 | 先 benchmark，有数据再改 |
| P4 | 风格、命名、注释 | 不单独做；随相关改动顺手 |

## 决策规则：直接做 vs 先提案

| 情况 | 动作 |
|---|---|
| Bug、文档漂移、测试缺口、示例失效 | 直接修 |
| 公开 API/配置/CLI 破坏性变更 | 先给方案 + 迁移说明，等确认 |
| 新抽象/新包/新依赖 | 先证明 2+ 调用方或明确边界 |
| 大重构 | 拆成小步，先给计划 |
| 性能优化 | 先给 benchmark/pprof 数据 |
| 纯风格改动 | 不做，除非同一次改动本来就要碰 |

## 包地图（改哪里）

| 包 | 职责 | 典型改动 |
|---|---|---|
| `fxkit.go` | Bundles / `Run` | 新增 bundle、调整默认装配 |
| `di/` | samber/do 封装、Lifecycle、group | 阶段、严格模式、provider 语义 |
| `config/` | YAML / Consul / flag 加载 | 优先级、校验、key |
| `server/` | chi mux、中间件、listener、health | 中间件顺序、CORS、限流、路由 |
| `huma/` | Huma API、registrar、`RegisterResource` | operation 注册、middleware、resource 约定 |
| `crudx/` | 受控列表 `q=` / cursor / `ListSpec` | 解析、JOIN、游标、限制 |
| `gormx/` | GORM client、事务、迁移 | 连接、事务、DDL |
| `outbox/` | 事务发件箱、relay、inbox | 分区发布、重试、幂等 |
| `hatchetx/` | Hatchet 封装 | workflow / actor / reminder / 事件 |
| `authz/` | JWT / Casbin / HTTPRoute | 鉴权中间件、策略、管理 API |
| `consulx/` | 注册、发现、锁 | TTL、advertise、Locker |
| `reqx/` | 出站 HTTP | 发现、failover、OTel |
| `docs/` `openapi/` | 文档与 spec | 合并、dump、encode |
| `mcpx/` | MCP / Function Calling | 路由提取、dispatcher |
| `cachex/` | Redis 缓存 | TTL、singleflight、负缓存 |
| `fxerrors/` | 对外错误 | 错误码、HTTP 映射 |
| `cli/` | Cobra 命令 | serve / openapi / mcp / validate / migrate |
| `cmd/fxkit/` | 脚手架 | init / new / gen resource / gen client |

## 实施流程

1. 读 `docs/guide.md` + 相关包 `README.md` + 对应 `skills/fxkit-*`。
2. Bug：先写失败测试；Feature：先写行为测试。
3. 最小改动；优先复用现有函数/类型。
4. 跑验证矩阵。
5. 同步文档与使用 skill。
6. 公开 API/配置/CLI 变化：加迁移说明，必要时 deprecation。

## 验证矩阵

| 改动 | 最低验证 |
|---|---|
| 任意 Go 代码 | `go test ./...` + `go vet ./...` + `gofmt -l .` |
| 并发/生命周期/锁/relay | `make test-race` |
| 配置/装配 | `FXKIT_STRICT_WIRING=1 go test ./...` + `make ci` |
| 公开 API/脚手架 | `cd example && go build ./... && go test ./...` |
| 文档/skill | 链接可达；命令/字段与代码一致 |

提交前跑 `make ci`（或 `make ci-full`，需要集成环境）。

## 文档与 skill 同步（硬要求）

| 改动 | 必须同步 |
|---|---|
| 公开 API / 行为 | `README.md` 对应章节 + `docs/*.md` + `skills/fxkit*/SKILL.md` |
| 新配置键 | `skills/fxkit/references/config-checklist.md` + 示例配置 |
| 新 CLI 命令 / flag | `skills/fxkit/SKILL.md` + `docs/guide.md` |
| 新包 / 删包 | `skills/README.md` + `skills/fxkit/references/packages.md` |
| 新使用主题 | 新建或更新 `skills/fxkit-*/SKILL.md`；不要塞进主 skill |
| 移除 / 弃用 | 文档 + 使用 skill + 迁移说明；不要静默删除 |

**没有同步文档/使用 skill 的框架改动，视为未完成。**

## 公开 API 兼容性

- 优先做加法；删除/改名走 deprecation，注释里写替代方案。
- 配置删除：在 `Validate` 明确拒绝（参考 `auth.deny_unregistered`），不要静默忽略。
- 生成代码 / 脚手架变化：更新 `cmd/fxkit/templates` 和对应 README。
- 行为变化：在 `README.md` 和 `docs/guide.md` 写清“之前/之后”。

## 评审清单

- [ ] 这个问题有证据吗（file:line / 测试 / benchmark / 文档 diff）？
- [ ] 能用现有函数/类型完成吗？
- [ ] 新接口是否有 2+ 实现或测试 fake？
- [ ] 新配置是否有真实调用方、默认值和 Validate？
- [ ] 校验是否只在边界？
- [ ] 错误是否只 wrap 一次？
- [ ] 是否新增了静默回退？
- [ ] 是否无测量地加了锁/重试/缓存？
- [ ] 测试是否覆盖失败路径和并发？
- [ ] 文档与使用 skill 是否同步？
- [ ] 公开 API 是否兼容？不兼容是否有迁移说明？
- [ ] 是否一次只做一件事，没有顺手重构？

公开 API / 架构判断建议按 `.cursor/skills/dual-review/SKILL.md` 走双模型复核。

## 反模式

- 为了优化而优化；没有证据就重构。
- 顺手重构、顺手改名、顺手升级依赖。
- 反射 DI、隐式注册、运行时扫描。
- god package：`utils` / `common` / `helpers` / `base` / `types`。
- 默认值/校验/文档各写一份。
- 配置错误静默回退。
- 内部函数到处 nil-check。
- 每层都 `fmt.Errorf("...: %w", err)`。
- 单实现接口、泛型 CRUD 包装器、`BaseService`。
- 为每个常量开 YAML 开关。
- 在 `Option` 闭包里读 group / 做副作用。
- 删除公开 API 不写迁移说明。
- 测试 fake 比真实服务“更宽容”，掩盖协议差异。
- opt-in 能力只接模块、不做可运行示例或失败路径。

## 什么时候可以加抽象

满足任一条件：

- 有 2+ 真实实现或 2+ 消费方；
- 测试确实需要 fake；
- 是外部依赖边界（DB/HTTP/MQ）；
- 有测量数据证明性能/并发需要。

否则先写具体代码。

## 给用户的优化建议格式

提出优化建议时用这个格式，避免泛泛而谈：

```text
问题：...
证据：file:line / 测试失败 / benchmark / 文档 diff
最小修复：...
风险：...
文档/skill 影响：...
验证：...
```

一次给 1–3 个高价值项，按 P0–P4 排序；不要列一堆低价值清单。

## 持续沉淀（后续对话）

后续对话里如果出现**可复用**的框架演进/优化建议、约束、反模式或评审结论，不要只留在对话里，按下面写回：

- **原则/约束** → 本 skill 的「两条硬原则」或「什么时候可以加抽象」。
- **具体包的行为/边界** → 「包地图」对应行。
- **评审经验** → 「评审清单」或「反模式」。
- **使用方式变化** → 对应的 `skills/fxkit-*/SKILL.md`。
- **公开行为/配置/CLI 变化** → `docs/guide.md` + `README.md`；配置键同步 `skills/fxkit/references/config-checklist.md`。

只沉淀经过验证、可复用的结论；一次性任务细节、临时方案、个人偏好不要写入。新增前先合并重复条目；如果新结论推翻旧条目，直接改旧条目，不要叠加矛盾。

## 维护本 skill 自身

- 保持简短；新增前先合并重复条目。
- 原则写“可判断”的规则，不写口号。
- 具体 API 细节放包 README / 使用 skill；本 skill 只放跨包的维护规则。
