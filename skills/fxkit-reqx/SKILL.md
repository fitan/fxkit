---
name: fxkit-reqx
description: >-
  Call downstream HTTP services with fxkit/reqx: Factory.Client, Consul healthy
  watch, Seeds fallback, failover on 502/503/504, otelhttp tracing, and
  TransportClient for OpenAPI-generated SDKs. Use when the user mentions reqx,
  imroc/req, Factory.Client, calling another service by Consul name, outbound
  HTTP, Seeds, MaxFailover, typed client, oapi-codegen, or "调下游" / "服务间 HTTP"
  on an fxkit app — even without saying Consul. Prefer this over raw http.Client
  or ad-hoc service discovery for fxkit services. For Consul self-register see
  fxkit-discovery.
---

# fxkit reqx

基于 `imroc/req/v3` 的出站 HTTP 客户端：Consul healthy watch + round-robin + 故障换节点 + OTel。

`reqx.Module` 已在 `fxkit.Default()`，注入 `*reqx.Factory`。

## 最小用法

```go
cli, err := factory.Client(reqx.ClientInput{
	Name:   "users", // Consul service name
	Scheme: "http",  // 默认 http；可 https
})
if err != nil {
	return err
}
resp, err := cli.R().SetContext(ctx).Get("/v1/users")
```

- 请求用**相对路径**；host 由 watch/Seeds 解析。
- **务必** `SetContext(ctx)`，才能 cancel / 传 trace。

## ClientInput

| 字段 | 默认 | 说明 |
|------|------|------|
| `Name` | — | Consul 服务名；与 Seeds 至少填一个 |
| `Seeds` | — | 静态端点兜底，如 `"127.0.0.1:8081"`、`"api.example.com:443"`；**本地用，生产靠 Consul** |
| `Scheme` | `http` | `http` / `https` |
| `Timeout` | 30s | 底层 `http.Client` |
| `PassingOnly` | true | 仅 healthy；`ptr(false)` 可放宽 |
| `OTel` | true | `otelhttp` 包装；`ptr(false)` 关闭 |
| `SpanName` | — | 可选覆盖 span 名 |
| `WatchWait` | 5m（上限 10m） | Consul blocking query 最长空闲挂起；index 变化立刻返回 |
| `MaxFailover` | 5 | 失败时最多尝试几个不同端点 |
| `BaseTransport` | — | 可选底层 RoundTripper |

## 行为

1. 有 `Name` + `discovery.consul_address`：阻塞 watch `/v1/health/service/{name}?passing=…`，端点来自 `Service.Address`（空则 `Node.Address`）+ Port。Consul 返回空列表时清空本地池，避免继续打已下线实例。
2. Round-robin 选节点发请求。
3. **传输错误**或响应 **502 / 503 / 504** → 换下一端点重试（至多 `MaxFailover`）。
4. 无 Consul 时可用 `Seeds` alone 跑通本地。

依赖配置：`discovery.consul_address`（见 `fxkit-discovery` / config checklist）。ACL：`CONSUL_HTTP_TOKEN`。

## Typed OpenAPI client

Huma 服务先 dump spec（`myapp openapi` / `make openapi SVC=…`），再在**调用方** `fxkit gen client`。`Factory.TransportClient` 返回 `*http.Client` + 逻辑 server URL（`http://<Name>`），transport 仍改写 Host。生成物带 `NewFromFactory`；也可手写：

```go
httpClient, server, err := factory.TransportClient(reqx.ClientInput{Name: "users"})
sdk, err := users.NewClientWithResponses(server, users.WithHTTPClient(httpClient))
```

不要用生成 SDK 替换本包：生成器没有 Consul watch / 502–504 failover。

## 与 consulx 的分工

| 包 | 方向 |
|----|------|
| `consulx` | **本服务**注册进 Consul（`fxkit-discovery`） |
| `reqx` | **调用** Consul 里其它服务 |

## Agent 检查清单

- [ ] 注入 `*reqx.Factory`，不要每次手建无发现的 `req.Client`（除非单测）。
- [ ] 生产用 `Name`，勿把实例 IP 写死在业务里（`Seeds` 仅本地）。
- [ ] 每次请求 `SetContext(ctx)`。
- [ ] 需要 https 时显式 `Scheme: "https"`。
- [ ] Consul 地址未配且无 Seeds → `Client` 会失败，先查 `discovery.consul_address`。
- [ ] 调用 Huma 下游的 typed SDK 用 `TransportClient` / `NewFromFactory`，不要把 base URL 写成实例 IP。
