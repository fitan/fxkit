---
name: fxkit
description: >-
  Build and extend Go microservices with the fxkit samber/do/v2 scaffold (config, chi HTTP,
  gormx, fxerrors, logx, buildinfo, cli, cmd/fxkit). Use whenever the user mentions fxkit,
  Fx modules, fxkit.Default/Minimal/Run, server.Route, ProvideMiddleware, gormx.Client,
  configs/config.yaml, or scaffolding a new fxkit service — even if they only say
  "加个服务" / "搭后端" without naming fxkit. Prefer this skill over inventing ad-hoc Fx
  wiring. Related: fxkit-huma-crud, fxkit-hatchet, fxkit-authz, fxkit-otel,
  fxkit-discovery, fxkit-reqx, fxkit-docs, fxkit-mcp, fxkit-layout.
---

# fxkit 服务脚手架

默认栈**不依赖 Dapr**。HTTP 由 `server.ListenerModule` 直接监听；异步/定时见 `fxkit-hatchet`。

Import 示例使用 `github.com/fitan/fxkit`——按消费方 `go.mod` / `replace` 替换。

## 何时读哪个 skill

完整包对照：`references/packages.md`。配置键：`references/config-checklist.md`。

| 任务 | Skill |
|------|--------|
| main、Module、配置、chi、gormx、fxerrors、CLI | **本 skill** |
| Huma REST / 列表 q= / crudx | `fxkit-huma-crud` |
| Events、outbox、cron、actor | `fxkit-hatchet` |
| JWT / Casbin / X-User | `fxkit-authz` |
| OTel / OTLP / 采样 / slog trace | `fxkit-otel` |
| Consul **注册**（consulx） | `fxkit-discovery` |
| 出站 HTTP / 调下游（reqx） | `fxkit-reqx` |
| Scalar /docs、openapi dump、`fxkit gen client` | `fxkit-docs` |
| MCP / Function Calling / OpenAPI→Tool | `fxkit-mcp` |
| 目录结构 / 多服务拆分 / 文件命名 / 依赖方向 | `fxkit-layout` |

## 设计原则（使用与维护都适用）

- **不过度设计**：没有第二个真实调用方，不抽接口、不加配置开关、不造通用层。先写具体实现。
- **不过度防御**：只在系统边界（HTTP/DB/外部服务）校验；内部信任调用方；配置错误启动失败，不静默回退。

## 新服务最小路径

1. CLI（可选）：`fxkit init myplatform --module github.com/me/myplatform`，再 `fxkit new myapp`（本地开发可在 init 加 `--fxkit-path`）。
2. `main`（脚手架已写入 `huma.Module`）：

```go
func main() {
	cli.SetRootName("myapp", "myapp service")
	fxkit.Run(fxkit.Default(), huma.Module, hello.Module)
}
```

3. 需要 Huma REST 时**显式**加 `huma.Module`（不在 `Default()` 内）：

```go
fxkit.Run(fxkit.Default(), huma.Module, users.Module)
```

4. 启动：`make infra`（可选中间件）后 `make run SVC=myapp`（或 `./myapp serve --port 8081`）。

内置：`/healthz` `/readyz` `/version` `/docs`（若挂 docs）。

## Bundle 选择

| Bundle | 内容 |
|--------|------|
| `fxkit.Default()` | config, buildinfo, otelx, server+Listener, cli, docs, gormx, hatchetx, outbox, authz, consulx, reqx, mcpx, cachex |
| `fxkit.Minimal()` | `Core()`：config, buildinfo, otelx, server+Listener, cli。不含 docs |
| `fxkit.Data()` | `Core()` + docs, gormx, outbox, cachex, consulx, reqx；不含 authz/MCP/hatchet |
| `fxkit.Worker()` | config, buildinfo, otelx, cli, gormx, outbox, hatchetx, cachex；无 HTTP listener |
| 自选 | `di.Options(config.Module, server.Module, server.ListenerModule, …)` |

## 业务模块写法

```go
var Module = fxkit.Service("hello",
	fxkit.Provide(func(i di.Injector) (*Service, error) {
		return NewService(di.MustInvoke[*gormx.Client](i))
	}),
	fxkit.Routes(helloRoutes()...),
	fxkit.ProvideValue(&Something{}),
	fxkit.ProvideConfig[HelloConfig]("hello"),
	fxkit.Invoke(func(i di.Injector) error { return di.MustInvoke[*Service](i).Seed() }),
)
```

- `Service(name, opts...)` → `di.Module`，只接受 [ServiceOption]。
- 构造函数统一 **`func(di.Injector) (T, error)`**；没有反射，接线错误编译期暴露。
- 贡献点：`Provide` / `ProvideValue` / `ProvideConfig` / `Routes` / `RoutesLazy` / `Invoke`。
- 多业务参数用 **struct 入参**（`CreateInput`），`context.Context` 单独放第一参。

### chi 路由

```go
func helloRoutes(svc *Service) []server.Route {
	return []server.Route{{
		Pattern: "/greet/{name}",
		Methods: []string{http.MethodGet},
		Handler: http.HandlerFunc(...),
	}}
}
```

- `Methods` 非空 → `chi.Method`（支持 `{id}`）。
- 前缀通配可用 `server.PrefixRoute("/prefix/", handler)`。
- 业务中间件：`server.ProvideMiddleware(fn)`（挂到共享 mux，顺序=Provide 顺序）。内置顺序：recover → SSE write-deadline → access-log(可选) → CORS → rate-limit → OTel/route-pattern(可选) → 业务中间件 → body-log(可选)。
- 错误用 `fxerrors.WriteError(w, r, err)`。

### fxerrors（对外错误）

| 构造 | 典型用途 |
|------|----------|
| `NotFound(resource, fmt, …)` | 404 |
| `Validation(fields)` | 400 字段级 |
| `BadRequest` / `Unauthorized` / `PermissionDenied` | 400/401/403 |
| `Conflict` / `Unprocessable` | 409/422 |
| `TooManyRequests` / `Timeout` / `Unavailable` | 429/504/503 |
| `Internal` / `Wrap(err)` | 500；`Wrap` 对外固定 `"internal error"`，保留 cause |

勿把内部 `err.Error()` 直接回给客户端。

### gormx

```go
return s.db.Transaction(ctx, func(txCtx context.Context) error {
	return s.db.Conn(txCtx).Create(u).Error
})
```

| API | 用途 |
|-----|------|
| `Conn(ctx)` | 请求级会话 / 事务内连接 |
| `Transaction(ctx, fn)` | 事务 |
| `Pool()` | 长生命周期 `*gorm.DB` |

未配 `db.driver` 时通常不连库；`/readyz` 在配了 DB 时会 ping。列表见 `fxkit-huma-crud`（`crudx.List` + `ListSpec`）。

框架自有表：`outbox_events` / `outbox_inbox_events`、`casbin_rule`、`authz_role`；opt-in：`idempotency_keys`、`audit_events`。DDL 用 `migrate print` 导出，或 `migrate baseline` 写成 Atlas 的第一份迁移。

## 配置约定

优先级：`--port` > Consul KV > 本地 YAML > defaults。配置树不从环境变量覆盖。

从 Consul 拉配置：

```bash
./myapp serve --consul localhost:8500 --consul-key config/myapp.yaml
```

扩展业务配置：不要把业务键塞进框架包。YAML 里加自己的顶层段，用 [ProvideConfig] 注入。非法值在 Load/Provide 时失败，不回退。

```yaml
orders:
  page_size: 20
```

```go
type OrdersConfig struct {
	PageSize int `yaml:"page_size"`
}

var Module = fxkit.Service("orders",
	fxkit.Provide(func(i di.Injector) (*Service, error) {
		return NewService(di.MustInvoke[*OrdersConfig](i)), nil
	}),
	fxkit.ProvideConfig[OrdersConfig]("orders"),
)

func NewService(cfg *OrdersConfig) *Service { ... }
```

非 Fx 场景用 `config.Load[OrdersConfig](cfg, "orders")`。

热重载：框架默认不自动刷新（`ProvideConfig` 是启动快照）。需要业务配置热更新时用 opt-in 的 `config.ProvideWatch[T](key)` 或 `(*config.Config).Watch[T](key)`；它只 watch Consul KV，框架配置仍建议重启/滚动发布。

## 可选运维能力

- **pprof**：`server.pprof.enabled: true`，默认关闭；开启后仍走 authz。
- **审计**：`audit.Module`（opt-in，放在 `authz.Module` 之后），sink 支持 `slog` / `db` / `outbox`；默认只记元数据。
- **幂等**：`idempotency.Module`；只对带 `Idempotency-Key` 的 mutating 请求生效，DB store；过期 key 按 `purge_interval` 清理。
- **限流**：统一在网关/边缘做；框架不再内置进程内限流。
- **乐观锁**：模型实现 `GetVersion/SetVersion`，用 `gormx.UpdateWithVersion`。
- **安全头/CSRF**：安全头默认开启；`server.csrf.enabled` 仅 cookie 认证。
- **配置热更新**：只提供 `config.Dynamic[T]` / `config.Watch[T]`；`ProvideConfig` 仍是启动快照；框架对象不热替换。

## 脚手架 CLI（`cmd/fxkit`）

```bash
fxkit init myplatform --module github.com/me/myplatform
fxkit init myplatform --fxkit-path /path/to/fxkit
fxkit new myapp
fxkit new myapp --minimal          # fxkit.Minimal() + Huma
fxkit new myapp --skip-tidy

fxkit gen resource Article \
  --field title:string:required \
  --field body:text \
  --search title,body \
  --filter author_id \
  --sort 'created_at desc'

fxkit gen client --spec openapi.yaml --out ./internal/clients/users
```

`gen resource` 产出对齐 crudx + 显式 Service 方法；接 Huma 时再读 `fxkit-huma-crud`。
`gen client` 读 OpenAPI（通常来自 `<svc> openapi`）生成 oapi-codegen SDK + `reqx` 封装；细节见 `fxkit-docs` / `fxkit-reqx`。

宿主扩展 Cobra：在 `Run` 前 `cli.AddCommand(...)` / `cli.SetRootName(...)`。内置：`serve`、`openapi`、`mcp`、`validate`、`migrate`（`print` / `baseline` / `check`）、`version`。

## logx / buildinfo（轻量）

- `logx`：默认彩色控制台 slog；`otel.enabled` 后由 otelx 安装带 OTLP fanout 的 handler。
- `buildinfo`：`/version` 与 `-ldflags` 注入；Consul 注册/补丁写入 `buildinfo.Meta()`（version、commit、git_*、go_version）。

## Agent 检查清单

- [ ] 用 `fxkit.Service` / `Routes`，不要手写零散 `di.Provide` 绕过惯例（除非扩展框架本身）。
- [ ] REST 需要时加入 `huma.Module`。鉴权在 chi 上，Huma 和裸路由一起过（见 `fxkit-authz`）。
- [ ] 对外错误走 `fxerrors`；Huma 非 `*fxerrors.Error` → 500 + `"internal error"`。
- [ ] Go 拉依赖：`GOPROXY=https://goproxy.cn,direct`（命令级 env）。
- [ ] 异步 → `fxkit-hatchet`；注册 → `fxkit-discovery`；出站 → `fxkit-reqx`；观测 → `fxkit-otel`；文档 → `fxkit-docs`。

## 反模式

- 在 `Default()` 里假设已有 Huma（没有）。
- 把业务状态放进 Hatchet actor 进程内存并假设 sticky（没有跨 run sticky）。
- 生产开启 `auth.dev_header_user`。
- 空 CORS 当成 `*`。
- 正式环境仍用 `docs` 默认 embed 骨架当 OpenAPI 契约。
- 以为裸 chi 不经过 authz。中间件挂在 chi 上；要匿名就写 `auth.public_paths`。
