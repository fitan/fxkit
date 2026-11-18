# fxkit 横切能力：决策记录

> 对应 [`gap-solutions.md`](./gap-solutions.md)。本文记录用户批注后的取舍与落地顺序。
> 原则不变：不过度设计、不过度防御；先有真实调用方再抽象。

## 决策汇总

| # | 能力 | 用户批注 | 调整后 |
|---|---|---|---|
| 1 | HTTP 幂等键 | 疑问：Key 是前端生成吗？outbox/inbox 够不够？ | 做（已实现，opt-in） |
| 2 | 乐观锁 | 疑问：常见吗？小概率？ | 做（已实现） |
| 3 | 审计日志 | **批准** | 做（opt-in） |
| 4 | 多租户 | 疑问：单租户怎么用？会不会复杂？ | 做（opt-in，已实现） |
| 5 | 限流粒度 | **批准** | 做（先 ip/header；subject/tenant 需后置） |
| 6 | 熔断/隔离/load shedding | 先不用做 | 暂不做 |
| 7 | DLQ/毒消息 | 可以做，SDK 集成方式 | 做（Go SDK，不做内置管理 UI） |
| 8 | 保留策略 | 可以加，注意并发自动处理 | 做（锁 + 批量 + 默认关闭） |
| 9 | 配置热更新 | 可以引入，Consul watch，每次读最新 | 只提供动态配置接口；框架对象不热更（`config.Dynamic[T]` / `Watch[T]`） |
| 10 | 安全头/CSRF | 疑问：讲讲作用 | 做（安全头默认开；CSRF opt-in） |
| 11 | pprof | 加上，可以做 | 做 |
| 12 | 错误追踪/SLO | 不做，文档写建议策略 | 不做代码，补文档 |
| 13 | 缓存批量失效 | 疑问：有过期时间，有什么用？ | 待定；有即时失效需求再做 |
| 14 | 读写分离/分库分表 | 不做，外部中间件 | 不做 |
| 15 | API 版本化 | 不做 | 不做 |
| 16 | i18n | 暂时不做 | 不做 |

## 疑问解答

### 1. Idempotency-Key 是前端生成吗？outbox/inbox 够不够？

- Key 由**调用方**生成：前端在用户点击“支付/下单”时生成 UUID，同一次业务操作的所有重试复用它；BFF/网关/内部调用同理。
- `outbox` 解决“业务写入 + 事件发布”的原子性；`inbox` 解决“消费者去重”。它们**不解决 HTTP 请求重试**：handler 会再次执行，可能再次产生副作用，也不会重放原始响应。
- 如果业务有天然唯一键（订单号），重复插入可靠唯一约束返回 409；但这只适合 create 型操作，不适合“转账/扣款”这类没有天然唯一键的 POST。
- 结论：有支付/下单/网关重试接口再做；否则先不做。

### 2. 乐观锁常见吗？

- 在多用户编辑场景很常见：后台管理、库存、工单、计费、配置编辑。单次概率低，但“丢更新”难排查。
- 单写者/事件驱动/append-only 的写路径可以不做。
- 成本很低：一个 `version` 列 + helper，opt-in 到需要的模型。
- 结论：有并发编辑页面就做；没有放 P2。

### 4. 多租户在单租户下怎么用？会不会增加复杂度？

- 单租户保持 `tenant.enabled=false`：零开销、零行为变化；`org_id` 可作为普通列保留，也可不保留。
- 启用后单租户也可注入固定 tenant，但会增加配置和约束；没有真实多租户需求不要开。
- 复杂度来自：每张表加 `org_id`、每个查询显式 `tenant.Scope`、跨租户测试。
- 结论：放 P2，等真的有第二个租户再设计。

### 10. 安全头有什么用？

| Header | 作用 |
|---|---|
| `X-Content-Type-Options: nosniff` | 禁止浏览器猜测 MIME，降低内容嗅探风险 |
| `X-Frame-Options: DENY` | 防点击劫持 |
| `Referrer-Policy` | 限制 Referer 泄露 |
| `Permissions-Policy` | 关闭不需要的浏览器能力 |
| `Strict-Transport-Security` | 强制后续请求走 HTTPS（HTTPS 稳定后开） |
| `Content-Security-Policy` | 限制脚本/样式/资源来源（主要对 HTML） |
| CSRF | 只有 cookie 认证需要；Bearer token API 不同 |

结论：安全头默认开（低风险）；HSTS/CSP 显式配置；CSRF 只在 cookie 场景加。

### 13. 缓存有过期时间，为什么还要批量失效？

- TTL 是最终一致：数据更新后旧缓存可能还要存活几分钟。
- 批量失效用于立即失效：后台改数据、批量导入、权限变更后，下一次读马上看到新数据。
- 能接受 TTL 内短暂陈旧就不需要；要 read-after-write 一致就需要 `DeletePrefix`。
- 结论：放 P2；先做 `DeletePrefix`，不做 tag 系统。

## 第 9 项：配置热更新的边界

用户希望依赖 Consul watch，每次读保持最新。方向可行，但要注意：

- 现在 `ProvideConfig[T]` 注入的是 `*T` 快照。要“每次读最新”，需要 provider 对象：
  ```go
  type Provider[T any] struct{ v atomic.Pointer[T] }
  func (p *Provider[T]) Get() *T
  ```
  所有消费方都要从 `*T` 改成 `*Provider[T].Get()`，这是破坏性变更。
- 建议新增 **opt-in** `config.Watch[T]`，只用于宿主业务配置；框架配置（server/db/auth/otel）仍保持启动时快照，避免运行时行为漂移。
- Consul watch + `atomic.Pointer` 本身不复杂；复杂的是校验、部分更新、回滚、哪些配置允许热更。
- 结论：可以做，但要单独设计，不要简单恢复 `Reload`。

## 落地顺序

1. **pprof**（最小、低风险）
2. **限流 key 扩展**（先 `ip` / `header:X-API-Key` / `route`；`subject/tenant` 需要后置限流）
3. **审计日志**（opt-in，metadata + DB/slog/outbox sink）
4. **outbox/inbox 保留 + DLQ SDK**（一起做，锁 + 批量删除）
5. **config.Watch[T]**（opt-in，Consul watch）
6. **文档/使用 skill 同步**

## 不做什么

- 不做支付、通知、搜索、文件存储、i18n、报表。
- 不做全自动多租户 scope。
- 不做全功能 API 网关。
- 不做分布式事务编排器（outbox + Hatchet 已够）。
- 不做无证据的性能改造。

## 本次落地状态

- [x] HTTP 幂等键（`idempotency.Module`）
- [x] 乐观锁（`gormx.UpdateWithVersion`）
- [x] 多租户（`tenant.Module` + `tenant.Scope`）
- [x] 安全头 / CSRF（`server.security_headers` / `server.csrf`）
- [x] `server` pprof（`server.pprof.enabled`，默认关闭）
- [x] 限流 key（`server.rate_limit.key_by` + `server.RateLimitKeyFunc`）
- [x] `audit` 包（metadata + slog/db/outbox sink，opt-in）
- [x] outbox/inbox DLQ 与保留（`ListFailed` / `PurgeFailed` / `ListStale` / `PurgeDone` + 自动清理循环）
- [x] `config.Watch[T]` / `config.ProvideWatch`（Consul KV 长轮询）
- [x] 文档与使用 skill 同步
- [~] 配置热更新：只提供 `config.Dynamic[T]` / `Watch[T]`；框架对象（listener/db/auth/otel）不热更，见 [`config-hot-reload-plan.md`](./config-hot-reload-plan.md)
- [ ] 熔断/bulkhead、读写分离、API 版本化、i18n（不做/观察）
