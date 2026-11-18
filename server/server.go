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
	var ssePrefixes []string
	if p.Server != nil {
		ssePrefixes = p.Server.SSEPathPrefixes
	}
	// recover 必须位于最外层，这样 CORS/OTel/body-log 中的 panic 才能被捕获。
	r.Use(recoverMiddleware)
	r.Use(sseWriteDeadlineMiddleware(ssePrefixes))
	if p.Server != nil {
		r.Use(securityHeadersMiddleware(p.Server.SecurityHeaders))
	}
	if p.Server != nil && p.Server.AccessLog {
		r.Use(accessLogMiddleware)
	}
	r.Use(corsMiddleware(p.Server))
	if p.Server != nil {
		r.Use(csrfMiddleware(p.Server.CSRF))
	}
	if p.Otel != nil && p.Otel.HTTPEnabled() {
		r.Use(otelMiddleware(ssePrefixes))
		r.Use(routePatternMiddleware)
	}

	for _, mw := range p.ExtraMiddlewares {
		if mw == nil {
			continue
		}
		r.Use(mw)
	}

	if p.Server != nil && p.Server.LogPayloads {
		r.Use(requestBodyLogMiddleware(ssePrefixes))
	}

	// pprof 是路由，必须在所有 r.Use 之后挂载（chi 限制）。
	if p.Server != nil && p.Server.Pprof.Enabled {
		mountPprof(r, p.Server.Pprof.Prefix)
	}

	return r
}

// RouterParams 收集 [RegisterRoutes] 消费的所有 [Route] 值。
type RouterParams struct {
	Mux    *chi.Mux
	Routes []Route
	// MaxBodyBytes 是默认请求体上限（来自 [Config.MaxBodyBytes]）。
	// 单条 [Route] 可用 MaxBodyBytes 覆盖；<=0 表示不限制。
	MaxBodyBytes int64
}

// RegisterRoutes 在共享 chi mux 上挂载 grouped [Route] 值。
//
// 非法条目（空 pattern / 缺前导斜杠 / nil handler / 未知 method）会让启动失败，
// 并一次性列出所有问题条目。chi 自己只会 panic 出
// "routing pattern must begin with '/' in ”"，堆栈无法定位是哪个模块贡献了坏路由；
// 而静默跳过会把装配期 bug 变成线上 404。这里两种都不选：先整体校验，
// 有任何问题就返回错误（调用方在 PreStart 里返回它，进程不启动）。
func RegisterRoutes(p RouterParams) error {
	if p.Mux == nil {
		return fmt.Errorf("server: RegisterRoutes called with nil mux")
	}
	if err := validateRoutes(p.Routes); err != nil {
		return err
	}
	slog.Info("──── route registration ────")

	registered := 0
	seen := make(map[string]struct{}, len(p.Routes))
	for _, route := range p.Routes {
		limit := route.MaxBodyBytes
		if limit == 0 {
			limit = p.MaxBodyBytes
		}
		h := limitRequestBody(route.Handler, limit)
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
				p.Mux.Method(m, route.Pattern, h)
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
		p.Mux.Handle(pattern, h)
		slog.Info("route", "pattern", route.Pattern)
		registered++
	}

	slog.Info("──── registration complete ────",
		"app_routes", len(p.Routes), "registered", registered)
	return nil
}

// validateRoutes 汇总所有非法路由，而不是遇到第一个就返回 —— 一次启动能看到全部问题。
func validateRoutes(routes []Route) error {
	var errs []error
	for i, r := range routes {
		if err := validateRoute(r); err != nil {
			errs = append(errs, fmt.Errorf("route[%d] (methods=%q pattern=%q): %w",
				i, r.Methods, r.Pattern, err))
		}
	}
	return errors.Join(errs...)
}

// validMethods 是 chi 接受的 HTTP method 集合（见 go-chi/chi Mux.Method）。
// 不在这里拦住的话，chi 会在 PreStart 里直接 panic。
var validMethods = map[string]struct{}{
	http.MethodConnect: {},
	http.MethodDelete:  {},
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodOptions: {},
	http.MethodPatch:   {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodTrace:   {},
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
		mm := strings.ToUpper(strings.TrimSpace(m))
		if mm == "" {
			return fmt.Errorf("empty method in %q", r.Pattern)
		}
		if _, ok := validMethods[mm]; !ok {
			return fmt.Errorf("unsupported method %q in %q (chi accepts GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS/CONNECT/TRACE)", m, r.Pattern)
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
					// 裸 chi 路由的请求体上限；Huma 路由有各自的 MaxBodyBytes。
					var maxBodyBytes int64
					if cfg, cfgErr := di.Invoke[*Config](i); cfgErr == nil && cfg != nil {
						maxBodyBytes = cfg.MaxBodyBytes
					}
					if err := RegisterRoutes(RouterParams{
						Mux:          mux,
						Routes:       di.GetGroup[Route](i),
						MaxBodyBytes: maxBodyBytes,
					}); err != nil {
						return fmt.Errorf("server: %w", err)
					}

					// 健康检查走 HealthChecker 接口，server 不再反向依赖 gormx。
					if err := registerHealthRoutes(i, mux); err != nil {
						return err
					}
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
			lc := di.MustInvoke[*di.Lifecycle](i)
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
		// 注意：此处刻意不做急切的 di.Invoke[*HTTPServer]。
		// NewMux 在构造时对 Route/Middleware group 做快照，因此在对象图装配
		// 期间解析 listener，会让 mux 在后续装配的模块（docs / mcpx / host
		// services）贡献各自的路由与中间件之前就被构建，从而把它们全部静默丢弃。
		//
		// listener 在 PreStart 阶段（PhaseMountHTTP 之后）解析，也就是在整个对象图
		// 已就绪、所有模块均已贡献、路由已挂载之后。NewHTTPServer 在此时注册其
		// bind/shutdown 钩子；Lifecycle.Start 在 PreStart *之后* 才对 lc.hooks
		// 做快照，因此该钩子必定会执行。
		//
		// 若改为在 Lifecycle.OnStart 钩子中解析，bind 钩子会被追加到快照之后，
		// 服务器将永远不会监听。
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
