# fxkit dashboard（Nuxt 4 + Nuxt UI）

`example/web` 是 fxkit showcase 的前端：Nuxt 4 + Nuxt UI dashboard 模板，已接入
catalog / storefront 与 Logto。

## 开发

```bash
pnpm install
pnpm dev          # http://127.0.0.1:3000
```

后端可以用 `example` 下的 `make demo`（PostgreSQL catalog + storefront）或
`make verify`（完整 Traefik/Consul 链路）。

## 页面

- `/`：文章统计 + 最近文章（catalog `GET /articles`）
- `/articles`：文章列表、搜索、创建、删除
- `/comments`：评论列表、按文章标题 JOIN 过滤、创建、删除
- `/digest`：storefront `GET /digest`（reqx → catalog）
- `/platform`：health/version、chi/fxerrors、Hatchet/outbox、cachex、authz、MCP、OpenAPI 探针 + 能力矩阵
- `/settings`：Logto 登录、X-User 切换、把 JWT sub 绑到管理员
- `/settings/security`：当前 API resource / claims / token 状态

界面文案均为中文；技术名词（JWT、MCP、OpenAPI 等）保留原文。

## 类型生成

前端类型来自 Go 服务导出的 OpenAPI，不手写 API DTO：

```bash
cd ..
make openapi SVC=catalog
make openapi SVC=storefront
make gen-web          # openapi-typescript → app/types/{catalog,storefront}.d.ts
```

`app/composables/useFxkit.ts` 用 `openapi-fetch` 创建 typed client，
`baseUrl: '/api'`，自动附加 `Authorization` 或 `X-User`。

## BFF 路由

浏览器只访问同源 `/api/*`，由 Nitro server routes 转发并透传
`Authorization` / `X-User`：

| 前端 | 上游 |
|------|------|
| `GET/POST /api/articles` | catalog `/articles` |
| `GET/DELETE /api/articles/:id` | catalog `/articles/:id` |
| `GET/POST /api/comments` | catalog `/comments` |
| `DELETE /api/comments/:id` | catalog `/comments/:id` |
| `GET /api/digest` | storefront `/digest` |
| `GET /api/me` | catalog `/me` |
| `POST /api/authz/bind` | catalog `PUT /authz/subjects/:sub/roles`（用 `X-User: alice`） |
| `/api/fxkit/*` | catalog 通用代理（version/health/chi/authz/cache-demo/mcp/openapi 等） |

上游地址在 `nuxt.config.ts` 的 `runtimeConfig.catalogBase` /
`storefrontBase`，可用 `NUXT_CATALOG_BASE`、`NUXT_STOREFRONT_BASE` 覆盖。

## Logto

`app/plugins/logto.client.ts` 使用 `@logto/browser`；重定向 URI 按当前 origin
生成。Logto 控制台需要登记 `:3000` 和 `:8880` 的 `/callback`（详见
`example/README.md`）。
