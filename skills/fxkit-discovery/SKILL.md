---
name: fxkit-discovery
description: >-
  Wire fxkit/consulx Consul self-registration: TTL health, advertise_address,
  metadata patch, and discovery.* config. Use when the user mentions Consul
  register, service discovery registration, advertise_address, TTL heartbeat,
  or "服务注册" on an fxkit service. For outbound HTTP to other services by
  Consul name, use fxkit-reqx (not this skill).
---

# fxkit consulx（服务注册）

`consulx.Module` 在 `fxkit.Default()`。出站调用下游见 **`fxkit-reqx`**。

## 自注册

```yaml
discovery:
  consul_address: "http://localhost:8500"
  consul_passing_only: true
  register: true                 # 无 sidecar 时打开
  advertise_address: ""          # 空 = 自动本机 IP
```

- `register: true`：以 `app.name` 注册，服务 ID 为 `{name}-{advertise}-{port}`（多副本同端口不冲突），TTL 心跳，HTTP 监听后再注册，停机注销。
- `register: true` 且无 `consul_address`：启动失败。仅 `register: false`（或未开注册）时地址空才跳过。
- Consul 不可达只告警；可对已存在同名同端口服务补丁 version/commit 等 buildinfo 字段。
- ACL：`CONSUL_HTTP_TOKEN`（注册、补丁、reqx watch 共用）。

从 Consul **拉配置**（config 包）：`--consul` + `--consul-key`。与 `discovery.consul_address` 相互独立。

## 调下游？

用 `fxkit-reqx`：`factory.Client(reqx.ClientInput{Name: "users"})`。

## Agent 检查清单

- [ ] 需要注册时设 `discovery.register: true` 与可达的 `consul_address`。
- [ ] Docker/多网卡留意 `advertise_address`，避免注册成不可达 IP。
- [ ] 出站 HTTP → `fxkit-reqx`，不要只改 consulx。
