// Package reqx 基于 [github.com/imroc/req/v3] 提供带 Consul 服务发现与 OpenTelemetry 的 HTTP 客户端。
//
// [Module] 已包含在 [github.com/fitan/fxkit.Default] 中。用法：
//
//	cli, err := factory.Client(reqx.ClientInput{Name: "users"})
//	resp, err := cli.R().SetContext(ctx).Get("/v1/users")
//
// [Factory.Client] 按服务名阻塞式 watch Consul `/v1/health/service/{name}?passing=true`
// （wait 默认 5m、上限 10m；超时或 index 变化都会返回，端点列表不变则不替换、不打 updated 日志）。
// 从 Service.Address（空则 Node.Address）+ Port 得到 IP 或域名端点；
// 请求使用相对路径，round-robin 选节点，传输失败或 502/503/504 时换节点重试；
// 出站 Transport 经 otelhttp 包装。Scheme 默认 http，可设 https。
//
// OpenAPI 生成客户端（oapi-codegen）请用 [Factory.TransportClient] 或
// `fxkit gen client` 产出的 NewFromFactory，而不是再包一层无发现的 http.Client。
package reqx
