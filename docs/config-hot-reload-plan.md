# 配置热更新方案

> 对应 `docs/gap-solutions.md` 第 9 项。状态：方案；`config.Watch[T]` 已实现，完整框架配置热更未做。

## 现状

- `config.Watch[T]` / `config.ProvideWatch[T](key)` 已实现：
  - 只 watch **Consul KV**（本地 YAML 不 watch）。
  - 初始值来自当前 `*config.Config`；变化时原子替换。
  - `Get()` 读当前快照；`Close()` 停止后台 watch。
- `ProvideConfig[T]` 仍是**启动快照**。
- 框架配置（`server` / `db` / `auth` / `otel`）默认不热更。

## 目标

- 业务配置可以热更新（如限流阈值、功能参数、外部 endpoint）。
- 框架配置默认不热更，避免运行时行为漂移。
- 更新失败保留旧值，不引入全局可变配置总线。

## 方案 A（推荐，当前实现）

业务模块：

```go
var Module = di.Module("orders",
    config.ProvideWatch[OrdersConfig]("orders"),
    func(i di.Injector) {
        w := di.MustInvoke[*config.Watch[OrdersConfig]](i)
        svc := NewService(w) // Service 内部用 w.Get()
        ...
    },
)
```

- `Watch.Get()` 返回当前快照；调用方只读。
- 只对业务段使用；框架段继续 `ProvideConfig`。
- 更新前可选校验（`Validate`）；失败保留旧值。
- 适合：限流参数、外部地址、功能开关等。

限制：

- 不做跨字段一致性/事务；每次只替换一个 section。
- 不监听本地 YAML；只适合 Consul KV。
- 旧指针仍有效，调用方不要缓存 `Get()` 结果过久。

## 方案 B（不推荐，除非明确需要）

把 `ProvideConfig[T]` 改成 `*config.Provider[T]`，所有消费者每次 `Get()`：

```go
type Provider[T any] struct{ v atomic.Pointer[T] }
func (p *Provider[T]) Get() *T
```

代价：

- 破坏性 API 变更（所有模块从 `*T` 改成 `*Provider[T]`）。
- 框架配置热更会让 listener/DB/auth/OTel 在运行时变化，难以测试和回滚。
- 需要新增指标、失败策略、版本/回滚机制。

如果确实要做，建议分阶段：

1. 新增 `Provider[T]`，保留 `ProvideConfig` 兼容。
2. 业务模块逐步迁移。
3. 框架配置逐项评估；`server` listener、`db` 连接、`auth`、`otel` 不建议热更。
4. 增加 `config.reload{key,result}` 指标；失败保留旧值；补断连/重连/非法值测试。

## 校验与回滚

- 更新前解析 + `Validate`；失败日志 + 保留旧值。
- 原子替换指针；读方无锁（`atomic.Pointer`）。
- 不做 `OnChange` 隐式副作用；需要时由业务显式订阅。
- 关闭时 `Close()` 取消 watch。

## 测试

- 正常更新（Consul index 变化）。
- 非法 YAML / 非法值：保留旧值。
- Consul 断连后重连。
- `Close()` 后不再更新。

## 决策

- **只提供动态配置接口**：`config.Dynamic[T]` / `config.Watch[T]`。
- `ProvideConfig[T]` 保持启动快照，**不变动**。
- listener / db / auth / otel 不做对象热替换；变更走重启/滚动发布。
- Casbin 策略 reload、JWKS 刷新等既有机制保持不变。
- 方案 B（把 `ProvideConfig` 改成 `Provider[T]`）**不做**。

如果要把框架配置也热更，先确认具体配置项、失败策略和回滚方式，再单独设计。
