# 重新判断「为旧故障留下」的代码

上次把几处复杂度留了下来，理由是注释里写过更简单的写法出过错。这次只看现在的代码做了什么，不把注释当成设计已经正确的证据。

## 1. 生命周期阶段：两段启动是对的，命名没有把偏移消掉

`di/lifecycle.go` 145–177 行先跑 PreStart，再给 `lc.hooks` 拍快照，然后才跑 OnStart。监听钩子是在 PreStart 里 `Append` 的，所以能被这次启动执行。这是对的。如果在 OnStart 里再 `Append`，快照会错过它，端口不会听。

阶段常量没有完成注释里说的那件事。`di/lifecycle.go` 22–33 行写早期的 `PhaseWireGroup+1`、`PhaseMountHTTP+1` 不可读、改一个值就会错位。代码里还在用偏移：

- `otelx/otel.go` 391 行：`PhaseResolveContributors-10`
- `server/server.go` 335 行：`PhaseMountHTTP+1`，在这里构造 `*HTTPServer` 并登记监听钩子
- `cli/cli.go` 219 行：同一个 `PhaseMountHTTP+1`，做启动预热
- `consulx/consul.go` 370 行：`PhaseAfterHTTPServer`（210）

`PhaseAfterHTTPServer` 是 210，不是 `PhaseMountHTTP+1`（201）。监听钩子和预热挤在同一个优先级上，谁先跑取决于 `Core()` 里模块的注册顺序。Consul 自注册能排在监听钩子后面，是因为 210 比 201 大，PreStart 时先执行 201 的 `Append`，再执行 210 的 `Append`。注释把这件事写成「先绑定端口再注册」（`consulx/consul.go` 362–368 行）。端口是在之后的 OnStart 里才 bind 的。顺序靠的是钩子追加次序，不是阶段名本身。

判断：不要拆掉 PreStart / OnStart。把还在用的 `-10` 和 `+1` 收成具名常量，并改掉 Consul 那句「先绑定端口」。这不是新调度器。

## 2. Outbox 租约是对的；分组里的 break 没有完成它自己的目标

`markPublished` / `markFailed` 的条件是 `id` + `lease_id` + `status=processing`（`outbox/relay.go` 453–457 行，以及 markFailed 的 `UPDATE ... WHERE`）。旧 worker 不能把别人已经发布的行改回去。心跳按这一批共用的 `lease_id` 刷新 `locked_at`。这段协议是认领队列该有的形状，不是多出来的防御。

`processPartitioned` 在当前事件没成功时 `break`（`outbox/relay.go` 301–306 行）。注释说这样后续事件就不会先被消费。代码只是这一批不再发它们。没发的行留在 `processing`，批次结束心跳也停了，`locked_at` 停在认领时。失败的组头被 `markFailed` 放回 `pending`，并写上 `next_attempt_at`。退避一旦超过 `claim_timeout`（默认 30 秒，退避会翻倍到 5 分钟），组尾先过期被领走，组头还在等。注释描述的故障，这条 `break` 只挡住了同一批里的下一次 `publishOne`。

判断：不要为了跨副本全序去写 JSON 认领条件。`break` 本身也不该再被当成已经修好的保序。若要让单个 relay 在失败之后仍按 id 发：`break` 时把本组没发的行用当前 `lease_id` 改回 `pending`，`next_attempt_at` 设成和刚失败的那条一样，不增加 `attempts`。下次到点时 `ORDER BY id` 会先领组头。多副本仍然不保证同 key 全序。

## 3. 列表查询这三处，读完代码仍然该留

`crudx/qapply.go` 234–250 行：一对多 JOIN 时按主键 `GROUP BY`，不按关联列排序，也不 `SELECT DISTINCT *`。这是 PostgreSQL 对 json 列和 `DISTINCT`/`ORDER BY` 的限制，不是额外框架。标识符校验挡的是会拼进 SQL 的列名。`crudx/cursor.go` 90–96 行拒绝非标量游标，并用 `VK` 区分时间和长得像时间的字符串。没有更短的写法还能得到同样的查询。

## 4. 出站请求：不重放 POST 是对的；提前读 body 不是

`reqx/transport.go` 71–75 行：非幂等方法 `attempts = 1`，除非带了 `Idempotency-Key`。这是该有的限制。

`ensureRewindableBody` 在这之前就执行（56 行），最多读 4MB（`rewindBody`，327 行起）。不会重试的 POST 也会被读进内存。回卷只为第二次发送服务，应该发生在已经决定 `attempts > 1` 之后。

## 5. Consul 续租规则是对的

`consulx/consul_lock.go` 509–567 行：404、连续两次续租失败，或距上次成功已超过 TTL，就认定锁丢了并停掉续租。Session 到期后本地还报告持有，另一台可以加锁。这条规则不是注释堆出来的。锁实现长，是因为自己调 Consul HTTP，不是因为失败条件写多了。不把续租收成「只打日志、继续当持有者」。

## 复核后修正

Consul 钩子顺序不靠 210 大于 201。`consulx/consul.go` 372 行在自己的 PreStart 里先 `Invoke` HTTPServer，bind 钩子这时追加，然后才追加注册钩子。OnStart 里 `Listen` 在注册之前，注释里的「先监听再注册」和运行时一致，不用改。

预热钩子在 `cli/cli.go` 141 行、容器造完之后登记，seq 晚于模块钩子。同为 201 时它总在监听钩子后面，不取决于 `Core()` 的书写顺序。真正和注释不符的是 `cli/cli.go` 209 行把预热写成「PreStart 的最后阶段」。210 的钩子在它之后，那里才第一次解析的服务不在预热里。

Outbox 的 `break` 和 `relay.go` 247–250 行的合同一致：只保证同一批，跨批重试可以乱序。不把「抄 next_attempt_at」当成修法。投递已成功但标记失败时，没有可抄的退避时间。

reqx 的回卷同时设置 `GetBody` 和 `ContentLength`（`reqx/transport.go` 352–359 行）。标准库靠 `GetBody` 在连接一个字节都没写出时重试。不能改成只在 failover 前才读 body。

具名常量如果要加，取值必须仍是 201 和 -10。README 在教 `PhaseMountHTTP+1`。

## 不改的

crudx 的白名单和游标、Outbox 的租约比较并交换、Consul 的丢锁判定、POST 不换端点重放。这些按代码本身成立，不是因为注释这么写。
