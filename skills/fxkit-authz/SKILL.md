---
name: fxkit-authz
description: >-
  Configure fxkit authz: JWT Bearer or X-User identity, Casbin path/method RBAC,
  HTTPRoute registration, and /authz admin APIs. Middleware is on chi and covers
  Huma and bare chi routes. Use when the user mentions auth, JWT, Casbin, RBAC,
  permissions, X-User, public_paths, bootstrap_role, or protecting REST endpoints.
---

# fxkit authz（JWT + Casbin）

## 设计锁定

| 项 | 行为 |
|----|------|
| 范围 | chi 中间件，覆盖 Huma、裸 chi、docs、MCP |
| 身份 | JWT Bearer，或 `auth.dev_header_user` 时的 `X-User` / `X-Org` |
| 授权 | Casbin：`Enforce(sub, dom, path, METHOD)`。关闭 Casbin 时登录即可。handler 内可用 `Subject.Has` 看 JWT scope |
| 策略库 | 默认与业务同库表 `casbin_rule`；多副本 `reload_interval`（默认 5s）重载内存 |
| 未登记路由 | **一律要登录**。Casbin 开则 Enforce，未匹配 403。匿名路径只写 `public_paths` |
| 组织 | `Subject.OrgID` 是 Casbin 的 domain。空 domain 拒绝。跨租户用 `dom="*"` |

## 启用

```yaml
auth:
  enabled: true
  issuer: "http://localhost:3001/oidc"
  audience: "https://api.example.com"
  casbin:
    enabled: true
    table_name: casbin_rule
    auto_register_routes: true
    bootstrap_role: admin
    bootstrap_users: [alice]
    reload_interval: 5s   # 0s = 关闭（单副本可关）
  # deny_unregistered 已删除；写成 true 会启动失败。按路由拒绝请开 casbin.enabled
  dev_header_user: true   # 生产必须 false
```

`authz.Module` 已在 `fxkit.Default()` 中；需 `huma.Module` 才有 REST 面。

## 登记受保护路由

`auto_register_routes` 会在启动时刮取已挂载的 Huma OpenAPI（含 `RegisterResource`），再与 `ProvideHTTPRoutes` 合并写入 Casbin。仍建议显式登记以便目录表有 Summary/Tags：

```go
authz.ProvideHTTPRoutes(
	authz.HTTPRoute{Method: http.MethodGet, Path: "/users"},
	authz.HTTPRoute{Method: http.MethodGet, Path: "/users/{id}"},
	authz.HTTPRoute{Method: http.MethodPost, Path: "/users"},
)
```

或从 Operation 生成（带 Summary/Tags，用于权限目录展示）：

```go
authz.ProvideHTTPRoutes(
	authz.HTTPRoutesFromOperations(
		huma.Operation{OperationID: "listUsers", Method: "GET", Path: "/users", Summary: "列出用户", Tags: []string{"Users"}},
	)...,
)
```

`{id}` → Casbin keyMatch2 的 `:id`。策略例：`p, admin, /users, *` + `g, alice, admin`。

`ProvideHTTPRoutes` 是可变参数；需要在 DI 阶段动态计算时用 `ProvideHTTPRoutesLazy(func(di.Injector) ([]HTTPRoute, error))`。

## Handler 取身份

```go
subj, ok := authz.SubjectFromContext(ctx)
```

数据层隔离：框架默认单租户；如未来需要多租户，请在数据层单独设计（RLS / 独立 schema / 独立实例）。

## 管理 API

Casbin 启用时自动挂载（需登录）：`/authz/permissions`、`/authz/roles`、`/authz/subjects/{id}/roles`、`/authz/policies` 等。也可用 `*authz.Enforcer` 的 `CreateRole` / `SetRolePermissions` / `SetSubjectRoles`。

## 本地联调

```bash
curl -H "X-User: alice" http://localhost:8090/users
curl -H "Authorization: Bearer <token>" http://localhost:8090/users
```

`dev_header_user` 开启时进程打 WARN；无 JWT validator 时残留 Bearer 不会挡住 `X-User`。CORS 默认允许头只有 `Content-Type` / `Authorization`，不含 `X-User`；显式加入会允许浏览器带 `X-User`，仅本地联调使用，生产避免。

## Agent 检查清单

- [ ] 新 Huma 路径已 `ProvideHTTPRoutes`，或依赖 `auto_register_routes` 从 OpenAPI 刮取。
- [ ] 生产关闭 `dev_header_user`。
- [ ] 多租户靠 Casbin domain（`Subject.OrgID`）。空 org 会被拒绝。
- [ ] 裸 chi 同样经过 authz。匿名路径写进 `auth.public_paths`。
- [ ] 公开接口写进 `auth.public_paths`，不要指望未登记即匿名。
- [ ] 按路由拒绝要开 Casbin。`deny_unregistered: true` 会启动失败。
