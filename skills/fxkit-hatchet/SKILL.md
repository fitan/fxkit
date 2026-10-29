---
name: fxkit-hatchet
description: >-
  Use Hatchet via fxkit/hatchetx as the async backbone: events as MQ, transactional
  outbox + inbox, multi-step DAG workflows, durable tasks, cron reminders, one-off delayed
  runs (ScheduleOnce), rate limits, run lifecycle management, and semantic actors (concurrency mailbox).
  Use when the user mentions Hatchet, hatchetx, outbox, PushEvent, WithWorkflowEvents, CreateReminder,
  ScheduleOnce, NewActor, actorId concurrency, durable workflow, DAG tasks, or reliable async processing.
  Read this before inventing Kafka consumer groups or reintroducing Dapr for async.
---

# fxkit Hatchet / Outbox

Hatchet = 持久化分布式工作流、任务调度与事件驱动引擎；**不是**带 consumer group 的传统队列。fxkit 用 event key ≈ topic、standalone task / workflow ≈ consumer。

## 快速配置

```yaml
hatchet:
  enabled: true
  host_port: "localhost:7077"
  server_url: "http://localhost:8080"
  worker_name: "my-worker"
  worker_slots: 100
  outbox_publisher: true
  otel: true
outbox:
  enabled: true
  poll_interval: 1s
```

- **Token**：配置 `hatchet.token` 或环境变量 `HATCHET_CLIENT_TOKEN`。Hatchet V1 SDK 自动读取 `HATCHET_CLIENT_*` 变量。
- **Server URL**：Cron / Reminder / Schedule 依赖 REST API（`hatchet.server_url` / `HATCHET_CLIENT_SERVER_URL`）。
- **TLS**：`hatchet.tls_strategy`（`none` / `tls` / `mtls`）。loopback 默认 `none`；针对远程明文 gRPC（如 Docker 内 Hatchet Lite `SERVER_GRPC_INSECURE=t`）需显式声明 `none`。
- **OTel Trace**：`hatchet.otel: true`（默认启用）会自动为 Worker 挂载 `hatchet.start_step_run` 追踪链路，且与 `otelx` TracerProvider 原生集成。

---

## 客户端能力概览与 Escape Hatch（逃生通道）

`*hatchetx.Client` 提供了全面的薄封装与直接逃生通道：

```go
// 1. 逃生通道：需要调用底层 SDK 任意未封装方法时直接使用 Raw()
rawSDK := client.Raw() // *hatchet.Client

// 2. 核心子客户端直接访问
client.Runs()        // *features.RunsClient (Run 状态查询、取消、回放、日志流订阅)
client.Schedules()   // *features.SchedulesClient (一次性定时执行)
client.Crons()       // *features.CronsClient (周期性 Cron)
client.RateLimits()  // *features.RateLimitsClient (全局限流规则管理)
client.Events()      // v0Client.EventClient (事件发布)
client.Workflows()   // *features.WorkflowsClient (工作流定义管理)
client.Workers()     // *features.WorkersClient (Worker 状态)
```

---

## 工作流触发与执行

### 1. 同步等待结果（Run）与异步触发（RunNoWait）

```go
// 同步阻塞直到工作流完成，获取返回值
res, err := client.Run(ctx, "order-workflow", input, hatchetx.WithRunPriority(1))

// 异步触发并立即返回 runId
runID, err := client.RunNoWait(ctx, "order-workflow", input)

// 批量并发触发
refs, err := client.RunMany(ctx, "batch-job", []hatchetx.RunManyOpt{...})
```

### 2. Run 生命周期管控（取消、回放、状态、流式日志）

```go
// 查询执行状态
status, err := client.GetRunStatus(ctx, runID)

// 获取完整详情
details, err := client.GetRun(ctx, runID)

// 取消正在执行的任务
err := client.CancelRun(ctx, runID)

// 失败任务重新回放
err := client.ReplayRun(ctx, runID)

// 实时订阅日志 / 事件流
streamChan, err := client.SubscribeToStream(ctx, runID)
```

---

## 调度：一次性定时（ScheduleOnce） vs 周期性提醒（Reminder）

| 模式 | API | 语义与场景 |
|------|-----|------------|
| **一次性定时** | `ScheduleOnce` / `CancelScheduled` | 精确未来时间点（`RunAt: time.Time`）触发一次。如：订单 30 分钟未支付自动关闭。 |
| **周期性 Cron** | `CreateReminder` / `UpsertReminder` | 规律周期重复执行（如每 15 秒、每小时）。支持 `in.Upsert` 避免多节点重启重复冲突。 |

### 一次性定时执行（ScheduleOnce）

```go
scheduledID, err := client.ScheduleOnce(ctx, hatchetx.ScheduleOnceInput{
    WorkflowName: "order-auto-cancel",
    RunAt:        time.Now().Add(30 * time.Minute),
    Input:        map[string]any{"orderId": "order-123"},
})

// 取消尚未触发的定时执行
_ = client.CancelScheduled(ctx, scheduledID)
```

### 周期性提醒（Reminder / Cron）

```go
cronID, err := hatchetx.UpsertReminder(ctx, client, hatchetx.CreateReminderInput{
    WorkflowName: "device-heartbeat-check",
    ActorID:      "device-99",
    ReminderName: "ping",
    Period:       "*/15 * * * * *", // 6位秒级或标准5位cron，亦支持 @hourly 等别名
    Upsert:       true,             // 自动清理同名旧定时器，节点重启安全
})

// 删除提醒
_ = hatchetx.DeleteActorReminder(ctx, client, "device-heartbeat-check", "ping", "device-99")
```

---

## 多步 DAG 工作流与持久化长任务（Durable Task）

通过 `ProvideWorkflow` / `ProvideWorkflows` 声明并自动注册进 shared worker：

### 1. 多步骤 DAG 工作流

```go
func ProvideOrderWorkflow(client *hatchetx.Client) hatchetx.WorkflowBase {
    wf := client.NewWorkflow("process-order")

    task1 := wf.NewTask("validate-inventory", func(ctx hatchetx.Context, in OrderInput) (InventoryResult, error) {
        return InventoryResult{Available: true}, nil
    })

    task2 := wf.NewTask("deduct-payment", func(ctx hatchetx.Context, in OrderInput) (PaymentResult, error) {
        return PaymentResult{Success: true}, nil
    }, hatchetx.WithParents(task1)) // 声明依赖

    return wf
}

// 在模块 DI 中一行注册：
var Module = di.Module("order",
    di.Provide(ProvideOrderWorkflow),
    hatchetx.ProvideWorkflowFn(func(c *hatchetx.Client) ([]hatchetx.WorkflowBase, error) {
        return []hatchetx.WorkflowBase{ProvideOrderWorkflow(c)}, nil
    }),
)
```

### 2. 持久化长任务（Durable Context：Sleep / WaitForEvent / Memo）

Durable Task 具备休眠不占线程、进程重启后自动恢复断点状态的持久化能力：

```go
func RegisterApprovalTask(client *hatchetx.Client) hatchetx.WorkflowBase {
    return client.NewStandaloneDurableTask("loan-approval-flow", func(ctx hatchetx.DurableContext, in LoanRequest) (LoanResult, error) {
        // 1. 无阻塞休眠 24 小时（不占协程，引擎自动换出，重启不丢状态）
        _, _ = ctx.SleepFor(24 * time.Hour)

        // 2. 等待外部事件唤醒
        _, _ = ctx.WaitForEvent("manager-approved", fmt.Sprintf("input.loanId == '%s'", in.LoanID))

        // 3. 幂等计算（Memo 缓存，回放时不重复调用外部扣费接口）
        rawMemo, err := ctx.Memo("charge-service-fee", func() (any, error) {
            return chargeFee(in.LoanID)
        })

        return LoanResult{Approved: true}, nil
    })
}
```

---

## 全局速率限制（Rate Limits）

在代码中动态调整 Hatchet 全局限流策略：

```go
// 限制发送邮件通道每分钟最多 120 次
err := client.UpsertRateLimit(ctx, "send-email-limit", 120, hatchetx.RateLimitMinute)

// 在任务定义中引用该限流规则
task := client.NewStandaloneTask("send-email", sendEmailFn,
    hatchetx.WithRateLimits(&types.RateLimit{
        Key:   "send-email-limit",
        Units: 1,
    }),
)
```

---

## 事件当 MQ（结合 Transactional Outbox + Inbox）

| MQ 概念 | Hatchet + fxkit 对等实现 |
|---------|-------------------------|
| topic | event key（如 `user-created`） |
| 事务生产 | `outbox.EnqueueTopicMsg`（同事务写入） → relay → `PushEvent` |
| 单消费者互斥 | **同名** task + 多 worker 实例并发争抢 |
| 多消费者扇出 | **不同** task 名订阅同一个 event key |
| 消费幂等 | 消费端 `inbox.Once`（At-least-once → Exactly-once） |

### 生产（事务安全）

```go
return client.Transaction(ctx, func(txCtx context.Context) error {
    if err := client.Conn(txCtx).Create(&user).Error; err != nil {
        return err
    }
    return outbox.EnqueueTopicMsg(txCtx, outbox.EnqueueTopicInput[UserCreatedEvent]{
        Store:          store,
        Topic:          "user-created",
        Msg:            UserCreatedEvent{UserID: user.ID, Email: user.Email},
        IdempotencyKey: "user-created:" + user.ID,
    })
})
```

### 消费（事件订阅 + Inbox 幂等去重）

```go
task := client.NewStandaloneTask(
    "users-on-user-created",
    func(ctx hatchetx.Context, ev UserCreatedEvent) (map[string]any, error) {
        err := svc.inbox.Once(ctx, outbox.OnceInput{
            Key:   "user-created:" + ev.UserID,
            Topic: "user-created",
            Fn:    func(ctx context.Context) error { return svc.Handle(ctx, ev) },
        })
        return map[string]any{"ok": err == nil}, err
    },
    hatchetx.WithWorkflowEvents("user-created"),
)
```

---

## 语义 Actor（并发并发邮箱模式）

`NewActor` 基于 Hatchet 的 `GROUP_ROUND_ROBIN` 与 `MaxRuns=1`（Key 为 `input.actorId`）：

- **保证**：同一 `actorId` 的所有请求强制排队串行；不同 `actorId` 跨节点并行。
- **与传统 Dapr Actor 的区别**：无进程内状态驻留（状态必须持久化在 DB/Redis 中）。
- **关键约束**：入参结构体**必须**能 JSON 序列化出 `"actorId"` 字段（嵌入 `hatchetx.ActorRef` 或使用 `json:"actorId"` 标签）。

```go
type CounterInput struct {
    hatchetx.ActorRef
    Delta int64 `json:"delta"`
}

actor := hatchetx.NewActor("catalog-counter", handleInc)
hatchetx.ProvideRegistrar(actor.Registrar())

// 调用
out, err := actor.Call(ctx, client, CounterInput{
    ActorRef: hatchetx.ActorRef{ActorID: "counter-42"},
    Delta:    1,
})
```

---

## 开发者与 Agent 检查清单

- [ ] 注册原生工作流优先使用 `hatchetx.ProvideWorkflow(wf)` 或 `ProvideWorkflows(...)`。
- [ ] 长时间等待或审批流使用 `DurableContext`（`SleepFor` / `WaitForEvent`），严禁使用内存 `time.Sleep`。
- [ ] 延迟一次性触发用 `ScheduleOnce`；循环心跳才使用 `CreateReminder` / `UpsertReminder`。
- [ ] 外部未封装的能力统一走 `client.Raw()` 逃生通道，无需自行破坏客户端生命周期。
- [ ] 事件消息严格结合 Outbox（生产者）与 `inbox.Once`（消费者），确保可靠消息传递与幂等。
- [ ] Actor 请求入参必须序列化出 JSON 字段 `actorId`。
