---
name: fxkit-docs
description: >-
  Set up fxkit API docs: Scalar /docs, static openapi, merging a base OpenAPI
  YAML with live Huma via docs.MergedModule / openapi.MergeBaseHuma, dumping
  spec with `<svc> openapi`, and generating typed Go clients with
  `fxkit gen client`. Use when the user mentions OpenAPI, Scalar, /docs,
  merging specs, API reference, client SDK, oapi-codegen, or "接口文档".
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

## 离线 dump（`openapi` 命令）

服务二进制（`fxkit.Run`）自带 `openapi` 子命令：启动与 `serve` 相同的 Fx 图，**不监听 HTTP**，把 live Huma spec 打到 stdout（slog 在 stderr）。默认 **3.0.3 YAML**（给 oapi-codegen）；`--spec 3.1` 为原生 Huma。

```bash
myapp openapi > openapi.yaml
myapp openapi --spec 3.1 --format json -o openapi.json
make openapi SVC=users
```

需要 `huma.Module`。OnStart（连库、AutoMigrate 等）仍会执行，只跳过 listen。

`openapi.Encode` / `openapi.FromHuma`：Encode 可选 3.0/3.1 与 YAML/JSON；FromHuma 始终 3.1 YAML。

## typed client（`fxkit gen client`）

在**调用方**生成，不要给被调服务自己再生成一份 Go SDK：

```bash
fxkit gen client --spec services/users/openapi.yaml --out ./internal/clients/users
```

产出 `client.gen.go`（oapi-codegen types+client）、`reqx.go`（`NewFromFactory`）、`oapi-codegen.yaml`、`generate.go`、拷贝的 `openapi.yaml`。

```go
cli, err := users.NewFromFactory(factory, reqx.ClientInput{Name: "users"})
resp, err := cli.GreetWithResponse(ctx, "world")
```

发现/failover/OTel 仍走 `reqx`；生成器只提供路径和类型。手动接入其它生成器时用 `factory.TransportClient`。

## Agent 检查清单

- [ ] 生产不要依赖默认 embed 骨架当正式契约。
- [ ] 有 Huma → 优先 `MergedModule` / `MergedRoutes`。
- [ ] OpenAPI 路径与 Scalar `data-url` 一致（默认 `/openapi.yaml`）。
- [ ] 鉴权相关 operation 的 Summary/Tags 有助于 `authz` 权限目录展示。
- [ ] 给其它服务/前端发 SDK：先 `<svc> openapi`（默认 3.0）再 codegen；服务间 Go 调用用 `fxkit gen client` + `NewFromFactory` / `reqx`，不要写死实例 IP。
