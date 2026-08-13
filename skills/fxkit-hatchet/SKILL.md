---
name: fxkit-hatchet
description: >-
  Use Hatchet via fxkit/hatchetx as the async backbone: events as MQ, transactional
  outbox + inbox, workflows, cron, and semantic actors (concurrency mailbox). Use when
  the user mentions Hatchet, hatchetx, outbox, PushEvent, WithWorkflowEvents, CreateReminder,
  NewActor, actorId concurrency, reliable messaging, or replacing Dapr pubsub/actors/jobs.
  Also use for "当 MQ 用" / 事件驱动 / 定时任务 on fxkit — even without saying Hatchet.
  Read this before inventing Kafka consumer groups or reintroducing Dapr for async.
---

# fxkit Hatchet / Outbox

Hatchet = 持久化任务与事件引擎；**不是**带 consumer group 的传统 MQ。fxkit 用 event key ≈ topic、standalone task ≈ consumer。

启用：

```yaml
hatchet:
  enabled: true
  host_port: "localhost:7077"
  worker_name: "my-worker"
  outbox_publisher: true
outbox:
  enabled: true
  poll_interval: 1s
```

Token：`hatchet.token` 或 `HATCHET_CLIENT_TOKEN`。V1 SDK 读 `HATCHET_CLIENT_*`；yaml 仅在 env 未设时注入。

## 事件当 MQ

| MQ | Hatchet |
|----|---------|
| topic | event key（如 `user-created`） |
| publish | outbox → relay → `PushEvent`，或直接 `Client.PushEvent` |
| 同 group 互斥 | **同名** task + 多 worker |
| 多 group 扇出 | **不同** task 名订同一 event |
| 幂等 | `outbox` IdempotencyKey + 消费端 `inbox.Once` |

**重要**：事件在 task **注册之前**到达不会补跑。Outbox 保证发出；worker 需已注册。

### 生产（事务性 outbox）

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

### 消费

```go
hatchetx.ProvideRegistrar(func(svc *Service) hatchetx.Registrar {
	return func(client *hatchetx.Client) ([]hatchetx.WorkflowBase, error) {
		task := client.SDK.NewStandaloneTask(
			"users-on-user-created",
			func(ctx hatchet.Context, ev UserCreatedEvent) (map[string]any, error) {
				err := svc.inbox.Once(ctx, outbox.OnceInput{
					Key:   "user-created:" + ev.UserID,
					Topic: "user-created",
					Fn:    func(ctx context.Context) error { return svc.Handle(ctx, ev) },
				})
				return map[string]any{"ok": err == nil}, err
			},
			hatchet.WithWorkflowEvents("user-created"),
		)
		return []hatchetx.WorkflowBase{task}, nil
	}
})
```

投递 **at-least-once**；`inbox.Once` 做业务至多一次。

`hatchet.outbox_publisher: false` 或 Hatchet 未启用时：outbox 行仍可写入，但 relay **不会**往 Hatchet 推（无 publisher 会报错/空转——启用 outbox 时请同时开 hatchet + publisher，或自备 `EventPublisher` 测试替身）。

## 注册 Worker

`hatchetx.ProvideRegistrar(...)` 汇入共享 worker。无 workflow 时不启 worker（仅 client 也可推事件 / Run）。

## Semantic Actor（不是完整 Dapr Actor）

`NewActor` = concurrency 邮箱：`input.actorId` + `MaxRuns=1` + `GROUP_ROUND_ROBIN`。

- 有：同 actorId 串行、异 id 可并行  
- 无：跨 run sticky、进程内激活；**状态必须外置**（DB / appkv）

入参必须 JSON 序列化出 `"actorId"`（嵌 `hatchetx.ActorRef` 或 `json:"actorId"`）。**仅 `GetActorID()` 不够**——CEL 读 JSON。

```go
type IncInput struct {
	hatchetx.ActorRef
	Delta int64 `json:"delta"`
}
actor := hatchetx.NewActor("fxkit-counter", handler)
hatchetx.ProvideRegistrar(actor.Registrar())
out, err := actor.Call(ctx, client, IncInput{
	ActorRef: hatchetx.ActorRef{ActorID: "counter-1"},
	Delta:    1,
})
```

## Reminder（周期 Cron）

`CreateReminder` = 动态 Hatchet cron，**弱于** Dapr Reminder：

- 仅周期（无 dueTime / 一次性）
- 宕机漏跑**不补**
- 非 upsert（Name+Expression 重复可能失败）

```go
id, err := hatchetx.CreateReminder(ctx, client, hatchetx.CreateReminderInput{
	WorkflowName: "fxkit-actor-reminder",
	ActorID:      "sched-1",
	ReminderName: "heartbeat",
	Period:       "15s", // 或 @every 15s / 标准 cron
})
_ = hatchetx.DeleteReminder(ctx, client, id)
```

`NormalizeCronExpression`：`@every 15s` → `*/15 * * * * *`；`@every 2d` 拒绝。

## Cron / Run

- 动态：`client.Crons().Create(...)`
- 触发 workflow：`client.RunNoWait(ctx, name, input)`
- 直接事件：`client.PushEvent(ctx, key, payload, metadata)`（无 DB 一致性要求时）

## Agent 检查清单

- [ ] 一种业务消费逻辑 = **一个** task 名；扇出才用多个名字。
- [ ] 可靠发布用 outbox；消费用 inbox。
- [ ] Actor 入参带 JSON `actorId`；状态外置。
- [ ] Reminder 按 cron 语义文档化，勿承诺 Dapr dueTime。
- [ ] 勿假设 consumer group / offset；用 task 名 + worker 池解释扩展。
