# fxkit 框架审查（复核后）

## 本次已按结论改完

框架尚未正式使用，公开语义直接改，不保留旧行为。多副本 per-key 全序没有做，合同改成单个 relay、同一批。

### Inbox

`outbox/inbox.go` 新增 `ErrInboxBusy`。`Once`（92–110 行）认领失败后不再返回 nil。`claim`（125–153 行）最多试两次：抢到返回 lease；读到 `done` 返回空 lease（`Once` 当成功）；行刚被 `release` 删掉则再认领一次；未过期 `processing` 返回 `ErrInboxBusy`。`done` 仍是 nil。回归：`outbox/inbox_two_phase_test.go` 的 `TestInbox_InProgressIsNotSuccess`。

### partition_field

`outbox/relay.go` 186–190 行把分组判断放到 `concurrency == 1` 之前，复用 `processPartitioned`。默认并发为 1 时，同 key 失败会停住该组，其他 key 继续发。注释和 README / production-checklist / config-checklist 改成：只保证单个 relay 的同一批，多副本不保证全序。回归：`outbox/relay_fix_test.go` 的 `TestRelay_PartitionFieldDefaultConcurrencyStopsOnFailure`。

### reqx

`reqx/transport.go` 的 `RoundTrip` 用 `failoverOrder`（129–158 行）的快照，起点仍 round-robin。不再 `next()` + `i--`。回归：`reqx/transport_test.go` 的 `TestResolveTransport_ShrinkingPoolDoesNotSpin`（池子在第一次响应后收成 1 个，调用次数必须是 3）。

### markFailed

`outbox/relay.go` 的 `UPDATE` 把两个 `CASE` 写在 `attempts = attempts + 1` 之前。PostgreSQL 上 `next_attempt_at` 用 `CAST(? AS timestamptz)`，避免 `THEN NULL ELSE ?` 被推断成 text。MySQL / SQLite 仍用裸 `?`（SQLite 的 `CAST AS timestamptz` 会把时间收成数字）。

### 复核第二轮之后又改的

- `Inbox.Once` 的 `release` / `markDone` 走 `terminalWriteContext`，调用方 ctx 取消不会把认领行留在 processing。
- 合同写明：跨批重试不保证同 key 顺序。不提前释放组尾，那会让组尾比还在退避的组头更早被领走。
- `openAPISpecPath` 只放行 `.json` / `.yaml` / `-3.0.json` / `-3.0.yaml`。`/openapi/v1`、`/openapi-apps` 继续鉴权。
- reqx 仍按进入请求时的快照试完。请求中途被摘掉的端点可能再拨一次，换来循环有上界。不在每次尝试前再查池子。

### 鉴权白名单

`authz/routes.go` 57–82 行：`/healthz`、`/readyz`、`/version` 只精确匹配；`/docs`、`/schemas` 保留自身和子路径；`.` / `-` 只给 `/openapi` 和 `/huma/openapi`。`authz/routes_test.go` 的 `TestSkipAuthzPath` 覆盖 `/docs-admin`、`/version-history`、`/openapi-3.0.json`。

### 注释

`outbox/doc.go` 不再写认领会递增 Attempts。

`go test ./outbox/ ./reqx/ ./authz/` 已通过。

故意不做：认领 SQL、生成列、尾部提前释放、限流器分片、readyz recover。


复核文件：本文件的上一版。复核结论是「需修正后再执行」。下面是改过的判断。逐条处置见文末。

只记会在正常部署里发生的正确性 bug。不改认领 SQL 的形态、不加熔断、不给健康检查包 recover。

## 判断 1：`Inbox.Once` 把「还在处理」当成成功

`outbox/inbox.go` 100–105 行：`tryClaim` 返回 false 就 `return nil`。119–150 行把 `done` 和「未过期的 processing」合成同一个 false。

`done` 返回 nil 是对的。未过期的 processing 也返回 nil，调用方会 ack。

会丢消息的路径是崩溃，不是 `Fn` 返回错误：

1. Worker A 认领后进程被杀。`Once` 没有机会把错误返回给 broker，认领行留在 processing。
2. broker 在 `inbox_claim_timeout`（默认 5 分钟，`outbox/module.go` 26–30 行，`outbox/config.go` 38–39、57 行）内重投。
3. Worker B 插不进去，CAS 也抢不到（`inbox.go` 138–150 行），`Once` 返回 nil，这次重投被 ack。
4. 超时之后没有人再投递。A 的 `Fn` 没跑完。

A 的 `Fn` 自己返回错误时不会丢：`Once` 把错误原样返回（108–114 行），A 这次投递会被 nack。上一版把这一步写成丢消息，是错的。

`Fn` 已成功但 `markDone` 失败（153–167 行）也不是丢消息：副作用已经发生。重试若得到 nil，只是没把行标成 done。

修法：`done` 仍返回 nil；未过期的 processing 返回可 `errors.Is` 的哨兵 `ErrInboxBusy`。CAS 没命中后再读一次状态：行已是 done 返回 nil；行不存在（对方刚 `release`，171–175 行）不要返回 nil，重新认领或返回 `ErrInboxBusy`。

代价要写进 `Once` 的注释，并改 README 里直接 `return inbox.Once(...)` 的示例（约 1173 行）、`docs/guide.md` 376 行、`skills/fxkit-hatchet/SKILL.md` 149 行、`example/services/catalog/internal/article/article.go` 233 和 254 行：

- 处理中的重投会变成一次失败运行，靠消费端重试，而不是被 ack 掉。
- `markDone` 失败后，在租约过期前重试会一直拿到 `ErrInboxBusy`。重试窗口短于 5 分钟时，这次运行会失败，但 `Fn` 其实已经成功。过期后会再执行一次 `Fn`（至少一次，`Fn` 本来就要能重入）。

`store_test.go` 254–286 行和 `inbox_two_phase_test.go` 没有断言「processing 返回 nil」。`done` 的 nil 不变。

## 判断 2：`partition_field` 在默认并发下不生效；多副本全序不要用认领 SQL 补

`outbox/relay.go` 187–201 行在 `PublishConcurrency == 1` 时直接串行发完并 return。分组逻辑在 206 行，默认走不到。`PublishConcurrency` 默认就是 1（`outbox/config.go` 53 行）。

所以只配了 `partition_field`、没把并发调到 1 以上的部署，失败后不会停住同 key 的后续事件：同一批里 seq=1 失败，seq=2 照样发出。`config.go` 21–26 行写的「默认 1（严格顺序）」和「同 key 按产生顺序」都没被这段代码实现。`TestRelay_PerKeyOrderStopsOnFailure`（`outbox/relay_fix_test.go` 116 行）把并发设成 2，没覆盖默认路径。

代码修法只有一处：先看 `partition_field`，再走已有的 `processPartitioned`。并发为 1 时它本来就是组间串行，失败会停住该组（301–306 行）。不要新写一套循环。

多副本仍然没有 per-key 全序。`claim.go` 70–102 行的 `SKIP LOCKED` 会领走同 key 更大的 id；A 提交之后，未过期的 processing 也会让 B 领到后面的行（`claim.go` 46 行的谓词）。这是合同写大了（`config.go` 40–44 行，`relay.go` 246–250 行）。

不上「更小 id 仍未完成则不认领」的 SQL，原因：

- `payload` 是二进制：PostgreSQL `BYTEA`、MySQL `LONGBLOB`、SQLite `BLOB`（`gormx/migrations/{postgres,mysql,sqlite}/0001_init.sql` 第 10 行，`outbox/model.go` 33 行）。直接 `->>` / `JSON_EXTRACT` / `json_extract` 会让认领报错，relay 停摆。
- 把同 key 所有更早的 pending 都算「挡住」时，连续 pending 每轮只能领 1 条。满批才立刻再领（`relay.go` 580 行），否则要等 `poll_interval`（默认 1s）。热点 key 吞吐掉到大约每秒 1 条。
- 若只挡「未过期 processing」和「还没到重试时间的 pending」，两个认领事务并发时，B 会把 A 还没提交的 seq=1 看成可领的 pending，放行 seq=2。
- `partition_field` 允许 `a.b`（`config.go` 75–78 行）。Go 侧用 gjson（`relay.go` 325 行），PostgreSQL 的 `->>'a.b'` 对不上。取不到字段时 Go 合成空字符串、归入同一组（322–326 行），SQL 里是 NULL，互不阻挡。
- 没有生成列时，这个相关子查询在积压上是逐行解析 JSON。

这些代价比现在的合同错误更大。合同改成：单个 relay、同一批内，同 key 按 id 顺序；失败则该 key 本批后续不发。多副本不保证顺序。不新增生成列，也不做 leader 选举。

`processPartitioned` 失败后把尾部留在 processing、直到租约超时，会拖慢同 key 的下一次重领。这是延迟，不是本批内的错序。默认路径修好之后如果还要缩短这段等待，再把未投的尾部用当前 `lease_id` CAS 回 pending（不增加 `attempts`）。不是这次必须做的。

## 判断 3：reqx 故障转移在端点池变小时会空转

`reqx/transport.go` 84–96 行用进入函数时的 `pool.len()` 快照 `n`。命中已尝试过的端点且 `len(tried) < n` 就 `i--` 再 `next()`。循环里不看 `ctx.Done()`。

`endpointPool.next`（`reqx/pool.go` 60–68 行）是进程内共享的 round-robin。Consul watch 会在请求中 `pool.replace`（`reqx/watch.go` 71、78 行）。

池从 `[A,B,C]` 收到 `[A,B]`、且 A 和 B 都已经试过时，`next()` 再也给不出未试过的端点，`len(tried)==2 < 3`，空转直到进程被杀掉。

不要用「连续跳过次数超过当前 `pool.len()` 就停」。`rr` 是全局的，高并发下别的请求会插队，连续几次抽到已试端点是正常的，那样会放过一个还能用的端点。

修法：对 `snapshot()` 里还没试过的端点逐个试，试完就返回已有的 `lastErr`。请求中途新出现的端点留给下一次请求。不改哪些状态码可以 failover。

## 判断 4：MySQL 上 `markFailed` 稳定地少算一次重试

`outbox/relay.go` 474–483 行用 map 同时写 `attempts = attempts + 1` 和两个 `CASE WHEN attempts + 1 >= ?`。

gorm.io/gorm v1.31.1（`go.mod`）在 `callbacks/update.go` 209–213 行对 map key `sort.Strings`。SET 顺序固定为 attempts、last_error、lease_id、locked_at、next_attempt_at、status。不是随机的。`attempts` 永远排在两个 CASE 前面。

MySQL 单表 UPDATE 从左到右赋值，后面的表达式看得到前面列的新值。两个 CASE 里的 `attempts + 1` 实际是旧值 + 2。`max_retries = N` 会在第 N−1 次失败就标成 failed，`next_attempt_at` 也提前变成 NULL。PostgreSQL 和 SQLite 看的是更新前的行，sqlite 测试看不出来。

修法：一条列顺序固定的 SQL，两个 CASE 写在 `attempts = attempts + 1` 之前。

## 判断 5：`skipAuthzPath` 把带 `-` / `.` 的业务路径放行

`authz/routes.go` 57–70 行对全部 7 个前缀都做了 `prefix+"."` 和 `prefix+"-"`。注释 55–56 行说这样能拦住 `/docs-evil`，实际 `/docs-evil`、`/docs-admin`、`/version-history`、`/healthz-internal`、`/schemas-private` 都会跳过鉴权。Huma 和 mux 都走这里（`authz/huma_middleware.go` 86 行，`authz/mux_middleware.go` 60 行）。`HTTPRoutesFromOpenAPI` 还会把它们从策略目录里拿掉（`routes.go` 300–302 行）。

`.` 和 `-` 只该用在 `/openapi` 和 `/huma/openapi` 上（`/openapi.json`、`/openapi-3.0.json`）。`/docs`、`/schemas` 保留精确匹配和 `prefix+"/"`（测试要 `/schemas/Foo`，`routes_test.go` 57 行）。`/healthz`、`/readyz`、`/version` 只精确匹配。`normalizeRequestPath` 已经去掉末尾斜杠（34–42 行）。

## 判断 6：`outbox/doc.go` 把认领写成会递增 Attempts

`outbox/doc.go` 11–12 行。`claim.go` 48–57 行的 `claimSetValues` 不改 `attempts`。`model.go` 20–24 行是对的。只改注释。

## 故意不改

- 投递至少一次。`markPublished` 失败后重投（`relay.go` 372–381 行）是合同。
- 一批共用一个 `lease_id`。标记时清掉 lease，心跳的条件带 `status = processing`（`relay.go` 443–444、457 行）。
- `PoliciesFromRoutes` 保留 `{id}`（`authz/register.go` 88–103 行）。写入时转换：`AddPolicies` 在 `authz/enforcer.go` 256 行，`SetRolePermissions` / `AddRolePermission` 在 `authz/rbac.go` 318、362 行。
- `WithTx` 上的 `AfterCommit` 立刻执行并打日志（`gormx/client.go` 78–96 行）。
- singleflight 在调用方取消后仍写缓存（`cachex/helper.go` 212–223 行）。`DoChan` 的结果 channel 有缓冲。
- `DevHeaderUser` / `AutoBindBootstrap` 只告警（`authz/huma_middleware.go` 26–33 行）。
- 不把认领合成一条 `UPDATE … RETURNING`。不把限流器分片（`server/ratelimit.go`）。不给 `/readyz` 的 checker goroutine 加 recover（`server/health.go` 107–124 行）。

## 复核处置

上一版复核（需修正后再执行）的逐条回应：

1. 判断 1 第 4 步：接受。`Fn` 返回错误会 nack，丢消息只发生在崩溃后的重投被 ack。已改。
2. `markDone` 失败不是丢消息：接受。已改成修复的代价，不再当作同一个丢消息证据。
3. 公开语义和示例要一起改：接受。`done` 仍为 nil。已写入判断 1。
4. CAS 失败后再读，读到 not found 不能当成功：接受，写进修法。
5. 默认 `PublishConcurrency=1` 走不到 `processPartitioned`：接受。这是判断 2 的代码修法。
6. payload 是二进制，上一版的 JSON SQL 会让认领失败：接受，该修法作废。
7. NOT EXISTS 要么把热点 key 打成每轮 1 条，要么在并发认领时放行后续行：接受这个矛盾。因此不上认领 SQL，改合同。这是和「必须用 SQL 补上多副本全序」的分歧，留给使用方决定要不要单独立项。
8. 嵌套路径、NULL 与空串、O(N²)、failed 行放行：都是不上这条 SQL 的理由，已写入判断 2。只做尾部释放解决不了顺序，上一版把两处修法并列是错的；尾部释放降为可选。
9. 判断 3 用连续跳过次数会在稳定池上提前放弃端点：接受。改为按 snapshot 挑未试过的端点。
10. 判断 4 列顺序是排序后固定的，不是随机：接受。bug 更确定，修法不变。
11. `skipAuthzPath` 被误当成设计：接受，升为判断 5。
12. `SetRolePermissions` 的行号在 `rbac.go` 不是 `enforcer.go`：接受，已改引用。结论不变。
