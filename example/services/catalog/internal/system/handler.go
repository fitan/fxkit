package system

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/hatchetx"
	fxhuma "github.com/fitan/fxkit/huma"
)

// pingOut 是匿名探针响应。
type pingOut struct {
	Body map[string]any
}

// meOut 返回当前 authz subject。
type meOut struct {
	Body map[string]any
}

// counterPath 是 /counters/{id} 的路径参数。
type counterPath struct {
	// ID 是 counter 的 actorId。
	ID string `path:"id"`
}

// incIn 是 counter actor 的手动触发请求。
type incIn struct {
	// ID 是 counter 的 actorId。
	ID   string `path:"id"`
	Body struct {
		// Delta 是本次增量。
		Delta int64 `json:"delta"`
		// HoldMs 是人为阻塞时长。
		HoldMs int `json:"holdMs,omitempty"`
	}
}

// counterOut 返回 counter 值。
type counterOut struct {
	Body IncOutput
}

// heartbeatOut 返回最近一次 reminder tick。
type heartbeatOut struct {
	Body HeartbeatOut
}

// cacheDemoOut 是 Get/Set 演示响应。
type cacheDemoOut struct {
	Body map[string]any
}

var (
	pingOp = huma.Operation{
		OperationID: "publicPing",
		Method:      http.MethodGet,
		Path:        "/public/ping",
		Summary:     "Anonymous ping (auth.public_paths)",
		Tags:        []string{"System"},
	}
	meOp = huma.Operation{
		OperationID: "getMe",
		Method:      http.MethodGet,
		Path:        "/me",
		Summary:     "Current authz subject",
		Tags:        []string{"System"},
	}
	getCounterOp = huma.Operation{
		OperationID: "getCounter",
		Method:      http.MethodGet,
		Path:        "/counters/{id}",
		Summary:     "Read actor-backed counter (DB, no Hatchet call)",
		Tags:        []string{"System"},
	}
	incCounterOp = huma.Operation{
		OperationID: "incCounter",
		Method:      http.MethodPost,
		Path:        "/counters/{id}",
		Summary:     "Increment via hatchetx.NewActor",
		Tags:        []string{"System"},
	}
	heartbeatOp = huma.Operation{
		OperationID: "getHeartbeat",
		Method:      http.MethodGet,
		Path:        "/heartbeat",
		Summary:     "Last hatchetx reminder tick",
		Tags:        []string{"System"},
	}
	heartbeatCallOp = huma.Operation{
		OperationID: "callHeartbeat",
		Method:      http.MethodPost,
		Path:        "/heartbeat",
		Summary:     "Trigger heartbeat actor once",
		Tags:        []string{"System"},
	}
	cacheDemoOp = huma.Operation{
		OperationID: "cacheDemo",
		Method:      http.MethodGet,
		Path:        "/cache-demo",
		Summary:     "cachex Get/Set demo",
		Tags:        []string{"System"},
	}
)

// Register 注册 system 探针与 actor 演示接口。
func Register(svc *Service) fxhuma.Registrar {
	return fxhuma.FuncRegistrar(func(api huma.API) {
		fxhuma.Register(api, pingOp, func(context.Context, *struct{}) (*pingOut, error) {
			return &pingOut{Body: map[string]any{"ok": true, "public": true}}, nil
		})
		fxhuma.Register(api, meOp, func(ctx context.Context, _ *struct{}) (*meOut, error) {
			subj, ok := authz.SubjectFromContext(ctx)
			if !ok {
				return nil, fxerrors.Unauthorized("no subject")
			}
			return &meOut{Body: map[string]any{
				"id":    subj.ID,
				"orgId": subj.OrgID,
			}}, nil
		})
		fxhuma.Register(api, getCounterOp, func(ctx context.Context, in *counterPath) (*counterOut, error) {
			out, err := svc.getCounter(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &counterOut{Body: out}, nil
		})
		fxhuma.Register(api, incCounterOp, func(ctx context.Context, in *incIn) (*counterOut, error) {
			if svc.hatchet == nil || !svc.hatchet.Enabled() {
				return nil, fxerrors.Unavailable("hatchet disabled")
			}
			out, err := svc.counter.Call(ctx, svc.hatchet, IncInput{
				ActorRef: hatchetx.ActorRef{ActorID: in.ID},
				Delta:    in.Body.Delta,
				HoldMs:   in.Body.HoldMs,
			})
			if err != nil {
				return nil, fxerrors.Unavailable("actor: %v", err)
			}
			return &counterOut{Body: out}, nil
		})
		fxhuma.Register(api, heartbeatOp, func(ctx context.Context, _ *struct{}) (*heartbeatOut, error) {
			out, err := svc.getHeartbeat(ctx)
			if err != nil {
				return nil, err
			}
			return &heartbeatOut{Body: out}, nil
		})
		fxhuma.Register(api, heartbeatCallOp, func(ctx context.Context, _ *struct{}) (*heartbeatOut, error) {
			if svc.hatchet == nil || !svc.hatchet.Enabled() {
				return nil, fxerrors.Unavailable("hatchet disabled")
			}
			out, err := svc.heartbeat.Call(ctx, svc.hatchet, HeartbeatIn{
				ActorRef:     hatchetx.ActorRef{ActorID: "showcase"},
				ReminderName: "manual",
			})
			if err != nil {
				return nil, fxerrors.Unavailable("heartbeat: %v", err)
			}
			return &heartbeatOut{Body: out}, nil
		})
		fxhuma.Register(api, cacheDemoOp, func(ctx context.Context, _ *struct{}) (*cacheDemoOut, error) {
			out, err := svc.cacheDemo(ctx)
			if err != nil {
				return nil, err
			}
			return &cacheDemoOut{Body: out}, nil
		})
	})
}

// humaHeader 演示 fxhuma.ProvideMiddleware（Huma 层中间件）。
func humaHeader() fxhuma.MiddlewareRegistrar {
	return fxhuma.MiddlewareFunc(func(_ huma.API) fxhuma.Middleware {
		return func(ctx huma.Context, next func(huma.Context)) {
			ctx.SetHeader("X-Fxkit-Huma", "1")
			next(ctx)
		}
	})
}

// systemHTTPRoutes 把 Huma 与裸 chi 路由统一登记到 authz 目录。
// pprof / MCP 也是非 Huma 路由，必须一起登记，否则 admin 也会 403。
func systemHTTPRoutes() []authz.HTTPRoute {
	routes := authz.HTTPRoutesFromOperations(pingOp, meOp, getCounterOp, incCounterOp, heartbeatOp, heartbeatCallOp, cacheDemoOp)
	// 裸 chi 路由不经过 Huma，HTTPRoutesFromOpenAPI 收不到；不显式登记的话，
	// Casbin 开启时连 admin 也会因为「未注册路由」被 403。
	return append(routes,
		authz.HTTPRoute{Method: http.MethodGet, Path: "/chi/echo", OperationID: "chiEcho", Summary: "Bare chi echo"},
		authz.HTTPRoute{Method: http.MethodGet, Path: "/chi/errors/{kind}", OperationID: "chiErrors", Summary: "Bare chi error writer"},
		authz.HTTPRoute{Method: http.MethodGet, Path: "/chi/tree/*", OperationID: "chiTree", Summary: "Bare chi prefix route"},
		// pprof 与 MCP 同样是框架直接挂载的非 Huma 路由。
		authz.HTTPRoute{Method: http.MethodGet, Path: "/debug/pprof", OperationID: "pprofIndex", Summary: "pprof index (server.pprof.enabled)"},
		authz.HTTPRoute{Method: http.MethodGet, Path: "/debug/pprof/*", OperationID: "pprofProfile", Summary: "pprof profile"},
		authz.HTTPRoute{Method: http.MethodPost, Path: "/debug/pprof/*", OperationID: "pprofSymbol", Summary: "pprof symbol"},
		authz.HTTPRoute{Method: http.MethodGet, Path: "/mcp", OperationID: "mcp", Summary: "MCP endpoint (mcp.enabled)"},
		authz.HTTPRoute{Method: http.MethodGet, Path: "/mcp/*", OperationID: "mcpSSE", Summary: "MCP SSE"},
		authz.HTTPRoute{Method: http.MethodPost, Path: "/mcp/*", OperationID: "mcpMessage", Summary: "MCP message"},
	)
}
