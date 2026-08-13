---
name: fxkit
description: >-
  Build and extend Go microservices with the fxkit Uber Fx scaffold (config, chi HTTP,
  gormx, fxerrors, logx, buildinfo, cli, cmd/fxkit). Use whenever the user mentions fxkit,
  Fx modules, fxkit.Default/Minimal/Run, server.Route, ProvideMiddleware, gormx.Client,
  configs/config.yaml, or scaffolding a new fxkit service — even if they only say
  "加个服务" / "搭后端" without naming fxkit. Prefer this skill over inventing ad-hoc Fx
  wiring. Related: fxkit-huma-crud, fxkit-hatchet, fxkit-authz, fxkit-otel,
  fxkit-discovery, fxkit-reqx, fxkit-docs.
---

# fxkit 服务脚手架

默认栈**不依赖 Dapr**。HTTP 由 `server.ListenerModule` 直接监听；异步/定时见 `fxkit-hatchet`。

Import 示例使用 `github.com/fitan/fxkit`——按消费方 `go.mod` / `replace` 替换。

## 何时读哪个 skill

完整包对照：`references/packages.md`。配置键：`references/config-checklist.md`。

| 任务 | Skill |
|------|--------|
| main、Module、配置、chi、gormx、fxerrors、CLI | **本 skill** |
| Huma REST / RegisterResource / crudx | `fxkit-huma-crud` |
| Events、outbox、cron、actor | `fxkit-hatchet` |
| JWT / Casbin / X-User | `fxkit-authz` |
| OTel / OTLP / 采样 / slog trace | `fxkit-otel` |
| Consul **注册**（consulx） | `fxkit-discovery` |
| 出站 HTTP / 调下游（reqx） | `fxkit-reqx` |
| Scalar /docs、base+Huma OpenAPI 合并 | `fxkit-docs` |

## 新服务最小路径

1. CLI（可选）：`fxkit new myapp --module github.com/me/myapp`（本地开发可加 `--fxkit-path`）。
2. `main`：

```go
func main() {
	cli.SetRootName("myapp", "myapp service")
	fxkit.Run(fxkit.Default(), hello.Module)
}
```

3. 需要 Huma REST 时**显式**加 `huma.Module`（不在 `Default()` 内）：

```go
fxkit.Run(fxkit.Default(), huma.Module, users.Module)
```

4. 启动：`./myapp serve --config configs/config.yaml`（或 `FXKIT_SERVER_PORT=8081`）。

内置：`/healthz` `/readyz` `/version` `/docs`（若挂 docs）。

## Bundle 选择

| Bundle | 内容 |
|--------|------|
| `fxkit.Default()` | config, buildinfo, otelx, server+Listener, docs, gormx, hatchetx, outbox, authz, consulx, reqx, cli |
| `fxkit.Minimal()` | config, buildinfo, server+Listener, docs, cli |
| 自选 | `fx.Options(config.Module, server.Module, server.ListenerModule, …)` |

## 业务模块写法

```go
var Module = fxkit.Service("hello", NewService,
	fxkit.Routes(helloRoutes),
	fxkit.Provide(NewSomething),
	fxkit.Invoke(seedOnStart),
)
```

- `Service(name, ctor, opts...)` → `fx.Module` + `Provide(ctor)`。
- `Routes(fn)` → `fn` 返回 `[]server.Route`。
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
- 业务中间件：`server.ProvideMiddleware(fn)`（挂到共享 mux，顺序=Provide 顺序）。内置顺序概念上：recover → CORS → OTel → body-log → 业务。
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

未配 `db.driver` 时通常不连库；`/readyz` 在配了 DB 时会 ping。列表/仓库见 `fxkit-huma-crud`（`crudx.List` / `crudx.NewRepo`）。

## 配置约定

优先级：`--port` > `FXKIT_*` env > Consul KV > 本地 YAML > defaults。

从 Consul 拉配置：

```bash
./myapp serve --consul localhost:8500 --consul-key config/myapp.yaml
# 或 FXKIT_CONFIG_CONSUL / FXKIT_CONFIG_CONSUL_KEY
```

扩展业务配置：

```go
func NewMyConfig(cfg *fxkit.Config) (*MyAppConfig, error) {
	var c MyAppConfig
	return &c, cfg.UnmarshalKey("myapp", &c)
}
```

热重载：`cfg.Reload()`。CORS：空 = **不开放**；只有显式 `["*"]` 才任意源。

## 脚手架 CLI（`cmd/fxkit`）

```bash
fxkit new myapp --module github.com/me/myapp
fxkit new myapp --minimal          # HTTP-only 倾向
fxkit new myapp --no-db
fxkit new myapp --fxkit-path /path/to/fxkit
fxkit new myapp --skip-tidy

fxkit gen resource Article \
  --field title:string:required \
  --field body:text \
  --search title,body \
  --filter author_id \
  --sort 'created_at desc'
```

`gen resource` 产出对齐 crudx + 显式 Service 方法；接 Huma 时再读 `fxkit-huma-crud`。

宿主扩展 Cobra：在 `Run` 前 `cli.AddCommand(...)` / `cli.SetRootName(...)`。

## logx / buildinfo（轻量）

- `logx`：默认彩色控制台 slog；`otel.enabled` 后由 otelx 安装带 OTLP fanout 的 handler。
- `buildinfo`：`/version` 与 `-ldflags` 注入；Consul 元数据补丁会用 version/commit。

## Agent 检查清单

- [ ] 用 `fxkit.Service` / `Routes`，不要手写零散 `fx.Provide` 绕过惯例（除非扩展框架本身）。
- [ ] REST 需要时加入 `huma.Module`；鉴权只覆盖 Huma（见 `fxkit-authz`）。
- [ ] 对外错误走 `fxerrors`；Huma 非 `*fxerrors.Error` → 500 + `"internal error"`。
- [ ] Go 拉依赖：`GOPROXY=https://goproxy.cn,direct`（命令级 env）。
- [ ] 异步 → `fxkit-hatchet`；注册 → `fxkit-discovery`；出站 → `fxkit-reqx`；观测 → `fxkit-otel`；文档 → `fxkit-docs`。

## 反模式

- 在 `Default()` 里假设已有 Huma（没有）。
- 把业务状态放进 Hatchet actor 进程内存并假设 sticky（没有跨 run sticky）。
- 生产开启 `auth.dev_header_user`。
- 空 CORS 当成 `*`。
- 正式环境仍用 `docs` 默认 embed 骨架当 OpenAPI 契约。
- 把裸 chi 路由鉴权当成框架自带（authz 只保护 Huma）。
