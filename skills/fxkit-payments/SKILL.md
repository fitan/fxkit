---
name: fxkit-payments
description: >-
  Build payment, order, ledger, or settlement services on fxkit with correctness
  primitives: money (minor units), double-entry ledger, payment state machine with
  CAS, idempotency keys, transactional outbox/inbox, webhook verification,
  reconciliation, and audit. Use when implementing payments/refunds/balances,
  provider webhooks, or when the user asks about idempotency, CAS, or ledger correctness.
---

# fxkit 支付正确性

完整说明见 [`docs/payments.md`](../../docs/payments.md)。

核心公式：

> **正确性 = 幂等 + 状态机（CAS） + 复式记账 + Outbox/Inbox + 对账 + 审计**

## CAS 是什么

先比较期望值，再原子更新：

```sql
UPDATE payments
SET status = 'authorized', version = version + 1
WHERE id = ? AND status = 'created' AND version = ?;
```

- `RowsAffected = 1` → 成功
- `RowsAffected = 0` → 已被别人改过 → 409

fxkit：

```go
err := gormx.UpdateWithVersion(ctx, db, &payment, "state")
err := payment.Apply(ctx, db, machine, &payment, "authorized")
```

## 原语包

| 包 | 用途 |
|---|---|
| `money` | 整数最小单位 + 币种；禁止 float |
| `ledger` | 复式记账：Transaction + Posting + Balance |
| `payment` | 状态机 + CAS |
| `webhook` | HMAC 验签 + 时间戳 + 原始 body |
| `reconciliation` | 内部/外部记录对账 |
| `outbox` / `inbox` | 事务性事件 + 消费去重 |
| `idempotency` | HTTP 幂等键 + 响应重放 |
| `audit` | 操作审计 |

## Money

```go
amount, err := money.New(1999, "CNY")
fee := amount.Mul(3)
total, err := amount.Add(fee)
```

- `Amount int64` 是最小单位（分）
- 不同币种不能相加/比较
- 金额只在服务端计算

## Ledger

```go
l := ledger.New(db)
err := l.Post(ctx, ledger.Transaction{ID: "payment:p1:authorized", Reference: "p1"},
    []ledger.Posting{
        {AccountID: "cash", Amount: 100, Currency: "CNY"},
        {AccountID: "merchant_payable", Amount: -100, Currency: "CNY"},
    })
```

- 同一事务按币种求和必须为 0
- 分录只追加，不 update/delete
- 余额：`l.Balance(ctx, "cash", "CNY")`
- 重复事务 ID → 409

## 状态机

```go
machine := payment.NewMachine(
    payment.Transition{From: "created", To: "authorized"},
    payment.Transition{From: "authorized", To: "captured"},
    payment.Transition{From: "created", To: "failed"},
)
err := payment.Apply(ctx, db, machine, &p, "authorized")
```

- 非法 transition → 409
- version 冲突 → 409
- 实体需要 `state` 列 + `version` 列 + `GetVersion/SetVersion`

## HTTP 幂等

```yaml
idempotency:
  enabled: true
  header: "Idempotency-Key"
  store: "db"
  purge_interval: 1h
```

- key 由调用方生成，同一次业务操作重试复用
- 渠道幂等键用内部支付 ID 派生，重试不重新生成

## Outbox / Inbox key

**key 是“事件实例”，不是实体 ID。**

| 事件 | Key |
|---|---|
| 创建 | `payment-created:{paymentID}` |
| 状态变更 | `payment:{paymentID}:{from}->{to}:v{version}` |
| 退款 | `refund:{refundID}` |
| 可重复操作 | 每次生成新的 operationID |

如果事件本来就允许重复且 handler 幂等，可以不设 key。

## Webhook

```go
raw, err := webhook.ReadBody(r, 1<<20)
v := webhook.Verifier{Secret: secret, Tolerance: 5 * time.Minute}
if err := v.VerifyRequest(r, raw); err != nil { ... }
// inbox.Once(providerEventID) 去重
// 状态机 CAS 流转
// ledger + outbox
```

- 必须用原始 body 验签
- 时间戳超窗拒绝
- 先持久化再 ack

## 对账

```go
diffs, err := reconciliation.Run(ctx, reconciliation.Comparator[Row]{
    Internal: loadLedger,
    External: loadProviderSettlement,
    Key:      func(r Row) string { return r.PaymentID },
    Equal:    func(a, b Row) bool { return a.Amount == b.Amount },
})
```

差异：`missing_external` / `missing_internal` / `mismatch`。  
修复动作必须幂等，建议用 Hatchet cron。

## 部分退款

- 支付单记录 `captured_amount` / `refunded_amount`；退款单独建 `payment_refunds` 表。
- `Refund(amount)` 可多次调用；省略 amount 表示退剩余额度。
- 不变量：`refunded_sum <= captured_sum`；超额退款返回 409。
- 每次退款一个账本事务：`payment:<id>:refunded:<refundID>`，避免重复记账。
- 退完才把状态转 `refunded`；部分退款时状态保持 `captured`，由 `refundedAmount` 表达进度。

## 提交后副作用

- 提交后副作用用 `gormx.AfterCommit(txCtx, fn)`：只有事务真正提交才执行，回滚不执行；回调 context 不带事务句柄。
- repository 放在共享 `internal/repository`（如 `payment.go`），对账、报表等其它 feature 可直接复用，不会 import `payments` 造成环。
- 多租户隔离模型已移除；框架默认单租户。

## ID 生成

- 用成熟库，不要手写随机 hex / 时间戳拼接。
- 需要**类型前缀**时用 TypeID（`go.jetify.com/typeid`）：`pay_ + Base32(UUIDv7)`，30 字符，时间有序；示例的支付单就是这么做的。
- 不需要前缀时用 UUIDv7（`github.com/google/uuid` 的 `NewV7`）即可。
- 前缀不增加碰撞空间；碰撞概率与排序性由底层 UUIDv7 决定，与字符串格式无关。
- 支付 ID 只生成一次，重试复用；渠道幂等键直接用支付 ID，不要每次重新生成。
- 存储：示例直接存 TypeID 文本（`size:64`）。若要解码成 `BINARY(16)`，注意前缀会丢失，需把 type 纳入主键或单独存类型列。

## 检查清单

- [ ] 金额用 `money`，不用 float
- [ ] 资金变更走 `ledger.Post`
- [ ] 状态变更走 `payment.Apply`
- [ ] HTTP 支付接口开 `idempotency`
- [ ] 事件走 outbox，消费走 inbox
- [ ] 渠道幂等键稳定
- [ ] Webhook 验签 + 去重 + 状态机
- [ ] 每日对账
- [ ] 审计关键操作
- [ ] 并发/重试/故障注入测试

## 反模式

- 直接 `UPDATE balance`
- 直接 `UPDATE status`，不走 CAS
- 用实体 ID 当所有事件的 key
- 在 DB 事务里调渠道
- 渠道重试重新生成幂等键
- Webhook 先反序列化再验签
- 用 float 存金额
- 声称 exactly-once

## 表

`ledger_accounts` / `ledger_transactions` / `ledger_postings`，DDL 见 `gormx/migrations/*/0006_ledger.sql`。
