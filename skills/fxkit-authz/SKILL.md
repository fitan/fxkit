---
name: fxkit-authz
description: >-
  Configure fxkit authz: JWT Bearer or X-User identity, Casbin path/method RBAC on Huma
  routes, HTTPRoute registration, and /authz admin APIs. Use when the user mentions
  auth, JWT, Casbin, RBAC, permissions, X-User, deny_unregistered, bootstrap_role, or
  protecting REST endpoints on fxkit. Also use when adding "登录鉴权" / "接口权限" to an
  fxkit Huma API. Remember authz covers Huma only — not bare chi handlers.
---

# fxkit authz（JWT + Casbin）

## 设计锁定

| 项 | 行为 |
|----|------|
| 范围 | **仅 Huma/REST**；裸 chi handler **不经** authz |
| 身份 | JWT Bearer，或 `auth.dev_header_user` 时的 `X-User` |
| 授权 | Casbin：`Enforce(sub, path, METHOD)`；关闭 Casbin 时回退 `HTTPRoute.Permission` + scopes |
| 策略库 | 默认与业务同库表 `casbin_rule` |
| 未登记路由 | 默认放行；`auth.deny_unregistered=true` → 403 |

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
  deny_unregistered: false
  dev_header_user: true   # 生产必须 false
```

`authz.Module` 已在 `fxkit.Default()` 中；需 `huma.Module` 才有 REST 面。

## 登记受保护路由

新增 Huma 路由时同步登记，否则 Casbin/`deny_unregistered` 行为不符合预期：

```go
authz.ProvideHTTPRoutes(func() []authz.HTTPRoute {
	return []authz.HTTPRoute{
		{Method: http.MethodGet, Path: "/users"},
		{Method: http.MethodGet, Path: "/users/{id}"},
		{Method: http.MethodPost, Path: "/users"},
	}
})
```

或从 Operation 生成（带 Summary/Tags，写入 `authz_api_permission` 目录）：

```go
authz.ProvideHTTPRoutes(func() []authz.HTTPRoute {
	return authz.HTTPRoutesFromOperations(
		huma.Operation{OperationID: "listUsers", Method: "GET", Path: "/users", Summary: "列出用户", Tags: []string{"Users"}},
	)
})
```

`{id}` → Casbin keyMatch2 的 `:id`。策略例：`p, admin, /users, *` + `g, alice, admin`。

## Handler 取身份

```go
subj, ok := authz.SubjectFromContext(ctx)
```

## 管理 API

Casbin 启用时自动挂载（需登录）：`/authz/permissions`、`/authz/roles`、`/authz/subjects/{id}/roles`、`/authz/policies` 等。也可用 `*authz.Enforcer` 的 `CreateRole` / `SetRolePermissions` / `SetSubjectRoles`。

## 本地联调

```bash
curl -H "X-User: alice" http://localhost:8090/users
curl -H "Authorization: Bearer <token>" http://localhost:8090/users
```

`dev_header_user` 开启时进程打 WARN；CORS 允许头**不含** `X-User`（防浏览器绕过）。

## Agent 检查清单

- [ ] 新 Huma 路径已 `ProvideHTTPRoutes`（或 auto_register 覆盖得到）。
- [ ] 生产关闭 `dev_header_user`。
- [ ] 勿对裸 chi handler 假设有 authz 中间件。
- [ ] 需要严管未登记路由时设 `deny_unregistered: true`。
