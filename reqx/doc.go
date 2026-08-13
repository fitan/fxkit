// Package reqx 基于 [github.com/imroc/req/v3] 提供带 Consul 服务发现与 OpenTelemetry 的 HTTP 客户端。
//
// [Module] 已包含在 [github.com/fitan/fxkit.Default] 中。用法：
//
//	cli, err := factory.Client(reqx.ClientInput{Name: "users"})
//	resp, err := cli.R().SetContext(ctx).Get("/v1/users")
//
// [Factory.Client] 按服务名阻塞式 watch Consul `/v1/health/service/{name}?passing=true`，
// 从 Service.Address（空则 Node.Address）+ Port 得到 IP 或域名端点；
// 请求使用相对路径，round-robin 选节点，传输失败或 502/503/504 时换节点重试；
// 出站 Transport 经 otelhttp 包装。Scheme 默认 http，可设 https。
package reqx
