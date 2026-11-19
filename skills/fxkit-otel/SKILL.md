---
name: fxkit-otel
description: >-
  Enable and tune fxkit/otelx OpenTelemetry (traces, metrics, logs, slog fanout,
  sampling, OTLP endpoint). Use when the user mentions otel, OpenTelemetry, OTLP,
  SigNoz, Jaeger, trace_id, runtime metrics, or enabling observability on an fxkit
  service — even if they only say "加监控" / "接链路追踪". Prefer this over wiring
  OTel SDK from scratch in fxkit apps.
---

# fxkit otelx

`otelx.Module` 已在 `fxkit.Default()`。默认关闭；`otel.enabled=true` 启用。

## 最小配置

```yaml
otel:
  enabled: true
  service_name: "myapp"
  service_version: "0.1.0"
  environment: "development"
  endpoint: "localhost:4317"   # 可写 https://host:4317，会去掉 scheme
  protocol: "grpc"             # grpc | http
  insecure: true
  sampling: "always_on"
  traces:
    enabled: true
    exporter: "otlp"           # otlp | stdout
  metrics:
    enabled: true
    exporter: "otlp"
    runtime_metrics: true
  logs:
    enabled: true
    exporter: "otlp"
```

## 行为要点

- 安装 Trace / Metric / Log Provider；slog 可 fanout 到 OTLP，并从活跃 span 注入 `trace_id` / `span_id`。
- Propagator：`TraceContext` + `Baggage`。
- HTTP 服务侧中间件由 `server` 挂 OTel（可随 otel 总开关）。匹配到 chi 路由后 span 名为 `METHOD {pattern}`（如 `GET /users/{id}`），避免按原始 URL 高基数。
- 出站：`reqx` 默认经 `otelhttp` 传播（见 `fxkit-reqx`）。
- Hatchet：SDK `Run()` 打 producer span；`hatchetx` 在 `hatchet.otel`（默认 true）时给 worker 挂 middleware（`hatchet.start_step_run`），复用本包 TracerProvider。见 `fxkit-hatchet`。
- Runtime metrics 挂在全局 MeterProvider，随 shutdown 结束。
- `service_version` 留空时回退 `buildinfo.Version`（未注入 `-ldflags` 时为 `dev`）。
- Endpoint 带 `http://` / `https://` 时会规范化为 `host:port`。

## 业务指标与 span

各子系统向同一个 MeterProvider 上报业务指标（未启用 otel 时是 noop）。完整清单见 README §13，常看的：

| 关注点 | 指标 |
|---|---|
| 消息可靠性 | `outbox.publish.results`、`outbox.event.lag`、`outbox.inbox.results{result=busy}` |
| 幂等 | `idempotency.requests{result=replayed\|conflict}` |
| 鉴权 | `authz.enforce.results`、`authz.jwks.fetch`、`authz.casbin.reload` |
| 资金 | `payment.transitions`、`ledger.posts`、`reconciliation.diffs` |
| 出站调用 | `reqx.requests{result=no_endpoints}`、`reqx.attempts` |
| 服务发现 | `consulx.register.ttl`、`consulx.lock.lost` |

消费 outbox 事件时，能拿到事件 metadata 就用 `outbox.ExtractTrace(ctx, metadata)` 续接发布方链路。

## 采样字符串

| 值 | 含义 |
|----|------|
| `always_on` / `always_off` | 全开 / 全关 |
| `trace_id_ratio:0.5` | 比例采样 |
| `parent_based_always_on` | 尊重父 span，根用 always_on |
| `parent_based_trace_id_ratio:0.5` | 尊重父 + 比例 |

## Agent 检查清单

- [ ] 生产用合理 sampling，勿长期 `always_on` 打爆 collector（除非明确需要）。
- [ ] `service_name` / `environment` 与部署一致，便于 SigNoz 过滤。
- [ ] 非本地 collector 时正确设 `insecure` / TLS。
- [ ] 业务自定义 span 用 `go.opentelemetry.io/otel` Tracer，勿另起一套 SDK。
