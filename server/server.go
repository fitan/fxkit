package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/otelx"
	"github.com/go-chi/chi/v5"
)

// MuxParams 是 [NewMux] 的输入。
type MuxParams struct {
	Server           *Config
	Otel             *otelx.Config
	ExtraMiddlewares []Middleware
}

// NewMux 构建共享 chi mux，默认中间件链为 CORS → OTel HTTP（启用时）→ route-pattern → recover，
// 并加上其他模块贡献的中间件。otelhttp 最多在此处安装一次。
func NewMux(p MuxParams) *chi.Mux {
	r := chi.NewRouter()

	slog.Info("──── http mux (middleware) ────")
	// recover must be outermost so panics in CORS/OTel/body-log are caught.
	r.Use(recoverMiddleware)
	r.Use(sseWriteDeadlineMiddleware)
	if p.Server != nil && p.Server.AccessLog {
		r.Use(accessLogMiddleware)
	}
	r.Use(corsMiddleware(p.Server))
	if p.Server != nil {
		r.Use(rateLimitMiddleware(p.Server.RateLimit))
	}
	if p.Otel != nil && p.Otel.HTTPEnabled() {
		r.Use(otelMiddleware)
		r.Use(routePatternMiddleware)
	}

	for _, mw := range p.ExtraMiddlewares {
		if mw == nil {
			continue
		}
		r.Use(mw)
	}

	if p.Server != nil && p.Server.LogPayloads {
		r.Use(requestBodyLogMiddleware)
	}

	return r
}

// RouterParams 收集 [RegisterRoutes] 消费的所有 [Route] 值。
type RouterParams struct {
	Mux    *chi.Mux
	Routes []Route
}

// RegisterRoutes 在共享 chi mux 上挂载 grouped [Route] 值。
//
// 非法条目（空 pattern / nil handler）会被跳过并告警，而不是让 chi panic ——
// panic 发生在 PreStart 里，堆栈只剩 "routing pattern must begin with '/' in ”"，
// 很难定位到底是哪个模块贡献了坏路由。
func RegisterRoutes(p RouterParams) {
	if p.Mux == nil {
		return
	}
	slog.Info("──── route registration ────")

	registered := 0
	seen := make(map[string]struct{}, len(p.Routes))
	for _, route := range p.Routes {
		if err := validateRoute(route); err != nil {
			slog.Error("route skipped: invalid entry", "pattern", route.Pattern, "error", err)
			continue
		}
		if len(route.Methods) > 0 {
			for _, m := range route.Methods {
				key := m + " " + route.Pattern
				if _, dup := seen[key]; dup {
					// chi 会静默用后注册的 handler 覆盖前一个（如 docs.Module 与
					// docs.MergedModule 同时存在）。这里至少让它可见。
					slog.Warn("route registered twice; the later handler wins",
						"method", m, "pattern", route.Pattern)
				}
				seen[key] = struct{}{}
				p.Mux.Method(m, route.Pattern, route.Handler)
				slog.Info("route", "method", m, "pattern", route.Pattern)
				registered++
			}
			continue
		}
		pattern := route.Pattern
		if pattern[len(pattern)-1] == '/' {
			pattern += "*"
		}
		if _, dup := seen["*"+pattern]; dup {
			slog.Warn("route registered twice; the later handler wins", "pattern", pattern)
		}
		seen["*"+pattern] = struct{}{}
		p.Mux.Handle(pattern, route.Handler)
		slog.Info("route", "pattern", route.Pattern)
		registered++
	}

	slog.Info("──── registration complete ────",
		"app_routes", len(p.Routes), "registered", registered)
}

// validateRoute 检查一条 [Route] 是否可挂载。
func validateRoute(r Route) error {
	if strings.TrimSpace(r.Pattern) == "" {
		return fmt.Errorf("empty pattern (check the providing module's ProvideRoutes/Route literal)")
	}
	if !strings.HasPrefix(r.Pattern, "/") {
		return fmt.Errorf("pattern must start with '/', got %q", r.Pattern)
	}
	if r.Handler == nil {
		return fmt.Errorf("nil handler for pattern %q", r.Pattern)
	}
	for _, m := range r.Methods {
		if strings.TrimSpace(m) == "" {
			return fmt.Errorf("empty method in %q", r.Pattern)
		}
	}
	return nil
}

// Runtime 控制 HTTP listener 行为。缺省则正常监听。
type Runtime struct {
	SkipListen bool
}

// ServerParams 列出 [NewHTTPServer] 的输入。
type ServerParams struct {
	Lifecycle *di.Lifecycle
	Server    *Config
	Mux       *chi.Mux
	Runtime   *Runtime
}

// HTTPServer 是 [http.Server] 的薄封装。
type HTTPServer struct {
	*http.Server
}

// NewHTTPServer 将 chi mux 绑定到支持 h2c 的 [http.Server]（配置端口），并通过生命周期 hook 管理 Start/Stop。
func NewHTTPServer(p ServerParams) *HTTPServer {
	if p.Server == nil {
		panic("server: nil Config")
	}
	port := p.Server.Port

	cfg := p.Server
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           p.Mux,
		ReadHeaderTimeout: durationOr(cfg.ReadHeaderTimeout, defaultReadHeaderTimeout),
		ReadTimeout:       durationOr(cfg.ReadTimeout, defaultReadTimeout),
		WriteTimeout:      durationOr(cfg.WriteTimeout, defaultWriteTimeout),
		IdleTimeout:       durationOr(cfg.IdleTimeout, defaultIdleTimeout),
	}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	if p.Runtime != nil && p.Runtime.SkipListen {
		slog.Info("http server listen skipped")
		return &HTTPServer{Server: srv}
	}

	if p.Lifecycle != nil {
		p.Lifecycle.Append(di.Hook{
			OnStart: func(_ context.Context) error {
				ln, err := newListener(srv.Addr)
				if err != nil {
					return err
				}
				slog.Info("http server listening", "addr", srv.Addr)
				go func() {
					if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
						slog.Error("http server error", "error", err)
					}
				}()
				return nil
			},
			OnStop: func(ctx context.Context) error {
				slog.Info("http server shutting down")
				return srv.Shutdown(ctx)
			},
		})
	}

	return &HTTPServer{Server: srv}
}

// Module 提供共享 chi mux 与最终 router。
//
// 关键时序：chi 要求所有中间件在注册任何路由之前声明，因此 mux（含中间件）
// 在 provider 内立刻构建；但 **路由注册推迟到 Lifecycle.PreStart**。
// 这样可以保证无论哪个模块先把 *chi.Mux 解析出来，最终的 Route group 都是
// 全部模块贡献完之后的完整集合 —— 装配期读取 group 只会拿到「已装配部分」。
var Module = di.Module("fxkit/server",
	config.Provide[Config]("server"),
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*chi.Mux, error) {
			cfg, _ := di.Invoke[*Config](i)
			otelCfg, _ := di.Invoke[*otelx.Config](i)
			mws := di.GetGroup[Middleware](i)
			mux := NewMux(MuxParams{
				Server:           cfg,
				Otel:             otelCfg,
				ExtraMiddlewares: mws,
			})
			return mux, nil
		})

		// 挂载路由与健康检查：必须在所有模块装配完成、且 huma 等 registrar
		// 已把路由装到 mux 之后执行（PhaseMountHTTP > PhaseWireGroup）。
		if lc, err := di.Invoke[*di.Lifecycle](i); err == nil && lc != nil {
			lc.AppendPreStartPhase(di.PhaseMountHTTP, di.Hook{
				OnStart: func(context.Context) error {
					mux, err := di.Invoke[*chi.Mux](i)
					if err != nil {
						return err
					}
					RegisterRoutes(RouterParams{
						Mux:    mux,
						Routes: di.GetGroup[Route](i),
					})

					// 健康检查走 HealthChecker 接口，server 不再反向依赖 gormx。
					registerHealthRoutes(i, mux)
					return nil
				},
			})
		}
	},
)

// ListenerModule 在共享 mux 上启动独立 HTTP listener。
var ListenerModule = di.Module("fxkit/server/listener",
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*HTTPServer, error) {
			lc, _ := di.Invoke[*di.Lifecycle](i)
			cfg, err := di.Invoke[*Config](i)
			if err != nil {
				return nil, err
			}
			mux, err := di.Invoke[*chi.Mux](i)
			if err != nil {
				return nil, err
			}
			rt, _ := di.Invoke[*Runtime](i)
			return NewHTTPServer(ServerParams{
				Lifecycle: lc,
				Server:    cfg,
				Mux:       mux,
				Runtime:   rt,
			}), nil
		})
		// NOTE: intentionally no eager di.Invoke[*HTTPServer] here.
		// NewMux snapshots the Route/Middleware groups at construction time, so
		// resolving the listener during graph assembly would build the mux before
		// modules assembled later (docs / mcpx / host services) contribute their
		// routes and middlewares, silently dropping every one of them.
		//
		// The listener is resolved during PreStart (after PhaseMountHTTP), i.e. once
		// the whole object graph exists, every module has contributed, and the
		// routes are mounted. NewHTTPServer registers its bind/shutdown hook at that
		// point; Lifecycle.Start snapshots lc.hooks *after* PreStart, so the hook is
		// guaranteed to run.
		//
		// Resolving it from a Lifecycle.OnStart hook instead would append the bind
		// hook after the snapshot and the server would never listen.
		if lc, err := di.Invoke[*di.Lifecycle](i); err == nil && lc != nil {
			lc.AppendPreStartPhase(di.PhaseMountHTTP+1, di.Hook{
				OnStart: func(context.Context) error {
					if _, err := di.Invoke[*HTTPServer](i); err != nil {
						return fmt.Errorf("server: start listener: %w", err)
					}
					return nil
				},
			})
		}
	},
)
