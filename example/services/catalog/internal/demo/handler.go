package demo

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/authz"
	fxhuma "github.com/fitan/fxkit/huma"
)

// 以下类型是 Huma 的输入/输出包装：字段名决定 path/query/body 的绑定方式。
type cacheIn struct {
	// Name 是缓存 key 后缀。
	Name string `path:"name"`
}

// cacheOut 等是各 demo 接口的响应包装，Body 字段直接决定 JSON 结构。
type cacheOut struct{ Body CacheDemo }

// configOut 返回动态配置快照。
type configOut struct{ Body ConfigDemo }

// lockIn 的 HoldMs 只是人为持锁时长，便于观察并发争锁。
type lockIn struct {
	// Name 是锁名（会被过滤成合法 key）。
	Name string `path:"name"`
	Body struct {
		// HoldMs 是人为持锁时长。
		HoldMs int `json:"holdMs,omitempty"`
	}
}

// lockOut 返回锁 key、fencing token 与实际持锁时长。
type lockOut struct{ Body LockDemo }

// outboxFailedIn 的 limit 走 query；out 包一层 items 便于以后加分页。
type outboxFailedIn struct {
	// Limit 是最多返回条数。
	Limit int `query:"limit"`
}

// outboxFailedOut 是 DLQ 列表响应。
type outboxFailedOut struct {
	Body struct {
		// Items 是 DLQ 行。
		Items []OutboxFailed `json:"items"`
	}
}

// requeueIn/out 对应 DLQ 重放接口。
type requeueIn struct{ Body RequeueOutboxReq }

// requeueOut 返回实际重置的行数。
type requeueOut struct{ Body RequeueOutboxResult }

// scheduleIn 与 scheduledOut 对应一次性定时运行。
type scheduleIn struct{ Body DemoInput }

// scheduledOut 返回一次性定时运行 ID，可用于查询/取消。
type scheduledOut struct {
	Body struct {
		// ScheduledRunID 是一次性定时运行 ID。
		ScheduledRunID string `json:"scheduledRunId"`
	}
}

// runIn/runOut 对应异步触发 DAG。
type runIn struct{ Body DemoInput }

// runOut 返回异步 workflow 的 runId。
type runOut struct {
	Body struct {
		// RunID 是异步 workflow 运行 ID。
		RunID string `json:"runId"`
	}
}

// rateLimitIn/okOut 对应 Hatchet 原生限流规则 upsert。
type rateLimitIn struct{ Body RateLimitReq }

// okOut 是「无返回值」操作的统一响应。
type okOut struct {
	Body struct {
		// OK 表示操作成功。
		OK bool `json:"ok"`
	}
}

// txIn 的 fail=true 用于演示事务回滚时 AfterCommit 不执行。
type txIn struct {
	// Fail 为 true 时强制回滚，用于验证 AfterCommit 不执行。
	Fail bool `query:"fail"`
}

// txOut 返回事务是否提交与 AfterCommit 实际执行次数。
type txOut struct{ Body TxDemo }

var (
	cacheDemoOp = huma.Operation{
		OperationID: "demoCacheGetOrSet",
		Method:      http.MethodGet,
		Path:        "/demo/cache/{name}",
		Summary:     "cachex.GetOrSet (singleflight + TTL jitter)",
		Tags:        []string{"Demo"},
	}
	configDemoOp = huma.Operation{
		OperationID: "demoDynamicConfig",
		Method:      http.MethodGet,
		Path:        "/demo/config",
		Summary:     "config.Watch / Dynamic current snapshot",
		Tags:        []string{"Demo"},
	}
	lockDemoOp = huma.Operation{
		OperationID: "demoLock",
		Method:      http.MethodPost,
		Path:        "/demo/lock/{name}",
		Summary:     "consulx.Locker with fencing token",
		Tags:        []string{"Demo"},
	}
	outboxFailedOp = huma.Operation{
		OperationID: "demoOutboxFailed",
		Method:      http.MethodGet,
		Path:        "/demo/outbox/failed",
		Summary:     "List outbox DLQ rows",
		Tags:        []string{"Demo"},
	}
	outboxRequeueOp = huma.Operation{
		OperationID: "demoOutboxRequeue",
		Method:      http.MethodPost,
		Path:        "/demo/outbox/requeue",
		Summary:     "Requeue outbox DLQ rows",
		Tags:        []string{"Demo"},
	}
	scheduleOnceOp = huma.Operation{
		OperationID: "demoScheduleOnce",
		Method:      http.MethodPost,
		Path:        "/demo/hatchet/schedule",
		Summary:     "hatchetx.ScheduleOnce (one-off)",
		Tags:        []string{"Demo"},
	}
	runDAGOp = huma.Operation{
		OperationID: "demoRunDAG",
		Method:      http.MethodPost,
		Path:        "/demo/hatchet/dag",
		Summary:     "Run two-step Hatchet DAG (no wait)",
		Tags:        []string{"Demo"},
	}
	runDurableOp = huma.Operation{
		OperationID: "demoRunDurable",
		Method:      http.MethodPost,
		Path:        "/demo/hatchet/durable",
		Summary:     "Run durable task with SleepFor (no wait)",
		Tags:        []string{"Demo"},
	}
	rateLimitOp = huma.Operation{
		OperationID: "demoRateLimit",
		Method:      http.MethodPost,
		Path:        "/demo/hatchet/rate-limit",
		Summary:     "Upsert Hatchet rate limit",
		Tags:        []string{"Demo"},
	}
	txDemoOp = huma.Operation{
		OperationID: "demoAfterCommit",
		Method:      http.MethodPost,
		Path:        "/demo/tx",
		Summary:     "gormx.AfterCommit (skipped on rollback)",
		Tags:        []string{"Demo"},
	}
)

// Register 注册全部 /demo/* 接口。依赖不可用时由 Service 返回 503，
// 而不是在装配期让整个进程启动失败。
func Register(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.Register(api, cacheDemoOp, func(ctx context.Context, in *cacheIn) (*cacheOut, error) {
			d, err := svc.CacheGetOrSet(ctx, in.Name)
			if err != nil {
				return nil, err
			}
			return &cacheOut{Body: d}, nil
		})
		fxhuma.Register(api, configDemoOp, func(context.Context, *struct{}) (*configOut, error) {
			return &configOut{Body: svc.DynamicConfig()}, nil
		})
		fxhuma.Register(api, lockDemoOp, func(ctx context.Context, in *lockIn) (*lockOut, error) {
			d, err := svc.LockDemo(ctx, in.Name, time.Duration(in.Body.HoldMs)*time.Millisecond)
			if err != nil {
				return nil, err
			}
			return &lockOut{Body: d}, nil
		})
		fxhuma.Register(api, outboxFailedOp, func(ctx context.Context, in *outboxFailedIn) (*outboxFailedOut, error) {
			items, err := svc.OutboxFailed(ctx, in.Limit)
			if err != nil {
				return nil, err
			}
			out := &outboxFailedOut{}
			out.Body.Items = items
			return out, nil
		})
		fxhuma.Register(api, outboxRequeueOp, func(ctx context.Context, in *requeueIn) (*requeueOut, error) {
			d, err := svc.RequeueOutbox(ctx, in.Body)
			if err != nil {
				return nil, err
			}
			return &requeueOut{Body: d}, nil
		})
		fxhuma.Register(api, scheduleOnceOp, func(ctx context.Context, in *scheduleIn) (*scheduledOut, error) {
			id, err := svc.ScheduleOnceDemo(ctx, in.Body)
			if err != nil {
				return nil, err
			}
			out := &scheduledOut{}
			out.Body.ScheduledRunID = id
			return out, nil
		})
		fxhuma.Register(api, runDAGOp, func(ctx context.Context, in *runIn) (*runOut, error) {
			id, err := svc.RunDAGDemo(ctx, in.Body)
			if err != nil {
				return nil, err
			}
			out := &runOut{}
			out.Body.RunID = id
			return out, nil
		})
		fxhuma.Register(api, runDurableOp, func(ctx context.Context, in *runIn) (*runOut, error) {
			id, err := svc.RunDurableDemo(ctx, in.Body)
			if err != nil {
				return nil, err
			}
			out := &runOut{}
			out.Body.RunID = id
			return out, nil
		})
		fxhuma.Register(api, rateLimitOp, func(ctx context.Context, in *rateLimitIn) (*okOut, error) {
			if err := svc.SetRateLimitDemo(ctx, in.Body); err != nil {
				return nil, err
			}
			return &okOut{Body: struct {
				// OK 表示操作成功。
				OK bool `json:"ok"`
			}{OK: true}}, nil
		})
		fxhuma.Register(api, txDemoOp, func(ctx context.Context, in *txIn) (*txOut, error) {
			d, err := svc.TxAfterCommit(ctx, in.Fail)
			if err != nil {
				return nil, err
			}
			return &txOut{Body: d}, nil
		})
	})
}

// demoHTTPRoutes 把 demo operation 登记到 authz 目录（Casbin 开启时 admin 才能访问）。
func demoHTTPRoutes() []authz.HTTPRoute {
	return authz.HTTPRoutesFromOperations(
		cacheDemoOp, configDemoOp, lockDemoOp, outboxFailedOp, outboxRequeueOp,
		scheduleOnceOp, runDAGOp, runDurableOp, rateLimitOp, txDemoOp,
	)
}
