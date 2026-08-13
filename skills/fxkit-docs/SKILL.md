---
name: fxkit-docs
description: >-
  Set up fxkit API docs: Scalar /docs, static openapi, and merging a base OpenAPI
  YAML with live Huma via docs.MergedModule / openapi.MergeBaseHuma. Use when the
  user mentions OpenAPI, Scalar, /docs, merging specs, or API reference for an
  fxkit service — even if they only say "接口文档".
---

# fxkit docs + openapi

`docs.Module` 在 `fxkit.Default()` / `Minimal()`，提供骨架：

| 路径 | 内容 |
|------|------|
| `GET /docs` | Scalar UI |
| `GET /openapi.yaml` | 静态 YAML |
| `GET /openapi.json` | 同上 JSON |

骨架 embed 的是**占位** spec。有 Huma 和/或自有 OpenAPI 时，应换成合并后的真实 spec。

## base + Huma 合并（推荐）

宿主嵌入自有 OpenAPI YAML，再用：

```go
docs.MergedModule(embedOpenAPI)
// 或
server.ProvideRoutes(docs.MergedRoutes(yamlBytes, humaAPI))
```

合并逻辑：`openapi.MergeBaseHuma`（内部常用 `openapi.FromHuma` + `MergeYAML`）。Scalar 看到**一份**统一文档。

仅 Huma：`MergedRoutes(nil, api)` 或只挂 live spec。

多份 YAML 手工合并：`openapi.MergeYAML(base, overlays...)`。

## Agent 检查清单

- [ ] 生产不要依赖默认 embed 骨架当正式契约。
- [ ] 有 Huma → 优先 `MergedModule` / `MergedRoutes`。
- [ ] OpenAPI 路径与 Scalar `data-url` 一致（默认 `/openapi.yaml`）。
- [ ] 鉴权相关 operation 的 Summary/Tags 有助于 `authz` 权限目录展示。
