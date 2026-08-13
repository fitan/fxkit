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
  advertise_address: ""          # 空 = 自动本机 IP；或 FXKIT_ADVERTISE_ADDRESS
```

- `register: true`：以 `app.name` 注册，TTL 心跳，停机注销。
- 地址空 → 跳过；Consul 不可达只告警，不拖垮启动。
- 可对已存在同名同端口服务补丁 version/commit 元数据。
- ACL：`CONSUL_HTTP_TOKEN`。

从 Consul **拉配置**（config 包）：`--consul` + `--consul-key`（或 `FXKIT_CONFIG_CONSUL*`）。

## 调下游？

用 `fxkit-reqx`：`factory.Client(reqx.ClientInput{Name: "users"})`。

## Agent 检查清单

- [ ] 需要注册时设 `discovery.register: true` 与可达的 `consul_address`。
- [ ] Docker/多网卡留意 `advertise_address`，避免注册成不可达 IP。
- [ ] 出站 HTTP → `fxkit-reqx`，不要只改 consulx。
