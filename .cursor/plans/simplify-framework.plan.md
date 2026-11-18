# 框架里该删的复杂度和该留的复杂度

问题：fxkit 有没有过度设计和过度防御，怎样更简洁。

结论：有三处是两套机制叠在一起，删掉一套框架会更小，行为更好讲。其余看起来厚的部分是在为已经发生过的故障付钱，不要为了「看起来简洁」拆掉。

框架还没正式使用，下面的删除可以改公开符号。不做的是重写 DI、拆掉 crudx、再给 outbox 加排序设施。

## 建议 1：鉴权只留 mux 一条路径

现在默认走 chi 中间件，`mux_middleware: false` 才装 Huma 中间件。

- 开关和两套语义：`authz/config.go` 34–41 行。`MuxMiddleware` 默认 true（67–70 行）。
- 装配分叉：`authz/module.go` 97–101 行。
- 两套中间件各自解析身份、各自决定公开路径：`authz/huma_middleware.go` 67–128 行，`authz/mux_middleware.go` 35–80 行。共用的只有 `authz/authorize.go` 16 行起的 `authorize`。
- 已经分叉：`OpPublic` 只在 Huma 层生效，mux 模式要另写 `PublicPaths`（`authz/config.go` 39–40 行）。

删掉 Huma 中间件、`MuxMiddleware`、`OpPublic`。匿名接口只写 `PublicPaths`。`authorize` 留下，给 mux 用。

不删 Casbin，不删管理 API。那是多租户授权本身，不是重复实现。

## 复核后收紧

建议 2 里「Casbin 关闭时管理接口靠 AdminPermission」不成立。`authz/admin_huma.go` 240–242 行在 Enforcer 未启用时直接 return，`/authz/*` 本来就不挂。`AdminPermission` 是死代码。

建议 3 不按原样做。`fxkit.go` 顶部 import 了全部子包，`Data()` / `Worker()` 还引用它们，改 `Default()` 减不了编译体积。脚手架 `main.go.tmpl` 用 `fxkit.Default()`，yaml 里仍有 `auth:` 和 `hatchet.outbox_publisher`。`serve` 不跑 `Unused()`。抽掉 authz 之后，把模板里的 `auth.enabled` 改成 true 会整站匿名；抽掉 hatchetx 之后，`outbox.enabled=true` 只打 Warn（`outbox/module.go` 95–97 行），事件堆在表里。

建议 4 不能只返回一个普通 `error`。`huma/errors.go` 20 行走 `fxerrors.Wrap`，非 `*fxerrors.Error` 会变成 `KindInternal`（`fxerrors/errors.go` 238–241 行）。负缓存命中会从 404 变成 500。判定「要不要写入负缓存」的 `errors.Is` 检测要留。

## 建议 2：Casbin 关掉时，不要再保留一套 Permission 字符串

`authz/authorize.go` 19–22、46–58 行：Casbin 开着走 Enforce；关着走 `HTTPRoute.Permission` 和 `DenyUnregistered`。`authz/routes.go` 27 行把 Permission 标成遗留字段。管理路由又给每条填 `AdminPermission`（`authz/admin_huma.go` 75–89 行），这样 Casbin 关闭时管理接口还靠另一套键。

结果是两套授权模型，修一个 bug 要对两套测试（`authz/huma_middleware_test.go` 的 DenyUnregistered / OpPublic，和 mux 的 Casbin 用例）。

收成一种：

- `auth.enabled` 且 Casbin 开：只 Enforce。管理 API 照常挂。
- `auth.enabled` 且 Casbin 关：只要求登录。不挂 `/authz/*` 管理接口（`authz/module.go` 102 行现在无条件 `ProvideRegistrarLazy`）。删掉 `Permission`、`DenyUnregistered`、`AdminPermission`。

不引入第三种「简单 RBAC」。登录不够时就开 Casbin。

## 建议 3：`Default()` 不要等于「全部模块」

`fxkit.go` 61–73 行把 hatchetx、authz、consulx、reqx、mcpx、cachex 都打进 `Default()`。这些模块默认关闭，但任何 `fxkit.Default()` 的进程都会编译并装配 Hatchet SDK、Casbin、Consul、MCP。`Data()`（85–96 行）和 `Worker()`（99–111 行）已经是按场景选模块。`Web()`（81–83 行）只是 `Default()` 的别名。`Minimal()`（76–78 行）只是 `Core()` 的别名，脚手架还在用（`cmd/fxkit/scaffold.go` 68 行）。

`Default()` 改成 Core + docs + gormx + outbox + cachex。hatchetx、authz、consulx、reqx、mcpx 由调用方显式加上，和现在的 `Data()` / `Worker()` 一样。删掉 `Web()`。`Minimal()` 留着，它是脚手架开关，不是第二套实现。

这是预设的收缩，不是把模块拆细。各模块内部的 no-op（没配 DSN 就不连库）保持不变，那是「依赖了模块但没配」的安全默认，不是重复设计。

## 建议 4：负缓存只返回一个错误

`cachex/negative.go` 45–163 行用四个 flag 重建 `gorm.ErrRecordNotFound`、`sql.ErrNoRows`、`pgx.ErrNoRows`、`fxerrors.KindNotFound`，再加字符串全等 `"record not found"`。`cachex/helper.go` 108–112 行把这件事写成 GetOrSet 的合同。

负缓存要留：它挡住穿透。合同收成 `errors.Is(err, ErrNegativeHit)`。删掉 `NegativeHitError` 的多哨兵 `Is`/`As` 和文案兼容。`Resolver`（`cachex/resolver.go`）留着，它是 yaml 默认 TTL 进到 GetOrSet 的那一层，删了 yaml 会再次不生效。

`cachex/singleflight.go` 里用 reflect 给自定义 Cache 找指针身份，不到 50 行，只有一种内置实现时可以以后再删。这次不动。

## 明确不简化

这些厚，但拆掉会把修过的故障请回来：

- `di/lifecycle.go` 11–33 行的 PreStart 阶段。路由、Huma、监听端口的顺序曾经靠 `PhaseWireGroup+1` 这种偏移，错一位就不监听。阶段名是这次收敛的结果，不是新抽象。
- outbox 的租约、心跳、Inbox 认领。至少一次投递需要比较并交换。`partition_field` 只保证单个 relay 的同一批，合同已经写明。不要再加认领 SQL，也不要为了「全序」删掉分组：分组的价值是不同 key 并行，不是更强的顺序。
- crudx 的白名单、标识符校验、游标类型标签、一对多 JOIN 不用 `DISTINCT *`。这是查询语言，方言分支是 PostgreSQL 真的会报错的地方。不再加运算符。
- reqx 对非幂等请求默认不换端点，以及 body 回卷上限。这是为了不把 POST 打到第二个实例。
- consul 锁的续租循环。Session TTL 不过期就丢锁。`FencingToken` 只有测试在读，先留在接口上，不为此再做一套存储校验。

注释里的「早期实现……」是考古，不是结构。改到哪段就删哪段，不单开一轮扫注释。

## 全量扫过、这次不改

按包看过公开入口和「两套做法」：

- `di`：PreStart 阶段留下。`ProvideLazy` / `ProvideLazyE` / `ProvideLazyOptionalE` 是跳过、报错、无错三种贡献，不是重复中间件。
- `config`：`Unused()` 只在 `validate` 跑。不把它接到 `serve`，否则业务 yaml 里多一个键就会起不来。
- `server`、`otelx`、`logx`、`fxerrors`、`buildinfo`、`cli`：没有第二套并行实现。
- `huma`：`Register` 和 `RegisterResource` 是手写路由和资源脚手架，不是两套鉴权。
- `docs` 与 `docs.MergedModule`：一份是活 spec，一份是和静态 YAML 合并，都要。
- `crudx`：`List` 包着 `ApplyList`。白名单、游标、JOIN 去重留下。
- `gormx`：AutoMigrate 和 schema contract 是开发和生产两条路，不是重复。
- `hatchetx`：对 Hatchet SDK 的薄别名留下。
- `consulx`、`reqx`、`mcpx`：留下。内存锁是测试实现，不是第二套生产锁。
- `Default()` 的模块列表不缩。`fxkit.go` 的 import 和 `Data()` / `Worker()` 仍然引用这些包，缩预设减不了二进制，还会让模板里的 `auth.enabled: true` 静默失效。

## 已经改的

- 删掉 Huma 鉴权中间件、`OpPublic`、路由匹配器和 `HTTPRoute.Permission`。鉴权只走 chi。Casbin 关时登录即可。`deny_unregistered: true` 在 `Config.Validate` 里启动失败；`false` 仍可留在模板里。`Subject.Has` 保留。管理 API 仍只在 Casbin 启用时挂上（原来就是这样）。
- 删掉 `fxkit.Web()`。`Minimal()` 和 `Default()` 的模块列表不动。
- 负缓存命中只认 `ErrNegativeHit`，同时 `errors.As` 仍是 `KindNotFound`，避免 Huma 把第二次请求打成 500。不再伪装 gorm/sql/pgx 哨兵，也不再把纯文案 `"record not found"` 写入负缓存。是否写入仍用 `errors.Is` 认这些哨兵。
- `outbox.enabled=true` 但没有 publisher 时启动失败，不再只打 Warn。

`go test ./authz/ ./cachex/ ./outbox/ ./huma/ .` 已通过。示例 catalog 的 platform 和 article 测试已通过。
