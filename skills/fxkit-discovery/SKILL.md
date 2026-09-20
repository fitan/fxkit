---
name: fxkit-discovery
description: >-
  Wire fxkit/consulx Consul self-registration, distributed locking (consulx.Locker),
  TTL health, advertise_address, metadata patch, and discovery.* config. Use when
  the user mentions Consul register, service discovery registration, distributed lock,
  Locker, WithLock, TryLock, advertise_address, TTL heartbeat, or "服务注册" / "分布式锁"
  on an fxkit service. For outbound HTTP to other services by Consul name, use fxkit-reqx.
---

# fxkit consulx（服务注册与分布式锁）

`consulx.Module` 已在 `fxkit.Default()` 中，提供服务自注册与 `consulx.Locker` 分布式锁工厂。出站调用下游见 **`fxkit-reqx`**。

## 自注册

```yaml
discovery:
  consul_address: "http://localhost:8500"
  consul_passing_only: true
  register: true                 # 无 sidecar 时打开
  advertise_address: ""          # 空 = 自动本机 IP
```

- `register: true`：以 `app.name` 注册，服务 ID 为 `{name}-{advertise}-{port}`（多副本同端口不冲突），TTL 心跳，HTTP 监听后再注册，停机注销。
- `discovery.tags`：额外 Consul 标签（与内置 `fxkit`/`http` 合并）。Traefik：`PathPrefix(/{app.name})` + file 里的 StripPrefix，网关路径与进程路由解耦。
- `register: true` 且无 `consul_address`：启动失败。仅 `register: false`（或未开注册）时地址空才跳过。
- Consul 不可达只告警；可对已存在同名同端口服务补丁 version/commit 等 buildinfo 字段。
- ACL：`CONSUL_HTTP_TOKEN`（注册、补丁、分布式锁、reqx watch 共用）。

从 Consul **拉配置**（config 包）：`--consul` + `--consul-key`。与 `discovery.consul_address` 相互独立。

## 分布式锁（Locker 抽象层）

Fx 容器已自动注入 `consulx.Locker`。底层基于 Consul Session（心跳续期与自动超时释放）+ KV 排他互斥。

### 快捷闭包 WithLock（推荐）

```go
type MyService struct {
	locker consulx.Locker
}

func (s *MyService) ProcessExclusive(ctx context.Context, jobID string) error {
	// 自动阻塞等待获取锁，执行业务并在完毕或 panic 时自动释放
	return s.locker.WithLock(ctx, "jobs/"+jobID, func(ctx context.Context) error {
		// 临界区业务代码
		return nil
	}, consulx.WithTTL(15*time.Second))
}
```

### 细粒度 Lock / TryLock 控制

```go
lock, err := locker.NewLock("leader-election",
	consulx.WithPrefix("locks/"),
	consulx.WithTTL(15*time.Second),
	consulx.WithValue([]byte("instance-1")),
)
if err != nil {
	return err
}

// 1. 非阻塞快速尝试获取锁
if err := lock.TryLock(ctx); err != nil {
	if errors.Is(err, consulx.ErrLockHeld) {
		// 锁已被其他实例持有
		return nil
	}
	return err
}
defer lock.Unlock(context.Background())

// 2. 检查是否依然持有锁（如检测到心跳中断导致 Session 丢失）
if !lock.IsHeld() {
	return consulx.ErrLockLost
}
```

### 单机 / 单元测试降级

本地单元测试或无 Consul 环境时，可使用内存锁实现测试互斥逻辑：

```go
memLocker := consulx.NewMemoryLocker()
```

## 调下游？

用 `fxkit-reqx`：`factory.Client(reqx.ClientInput{Name: "users"})`。

## Agent 检查清单

- [ ] 需要注册时设 `discovery.register: true` 与可达的 `consul_address`。
- [ ] Docker/多网卡留意 `advertise_address`，避免注册成不可达 IP。
- [ ] 分布式锁注入 `consulx.Locker`，优先使用 `WithLock` 防止锁泄漏。
- [ ] 出站 HTTP → `fxkit-reqx`，不要只改 consulx。
