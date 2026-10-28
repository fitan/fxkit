package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
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
	r.Use(corsMiddleware(p.Server))
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
func RegisterRoutes(p RouterParams) {
	if p.Mux == nil {
		return
	}
	slog.Info("──── route registration ────")

	for _, route := range p.Routes {
		if len(route.Methods) > 0 {
			for _, m := range route.Methods {
				p.Mux.Method(m, route.Pattern, route.Handler)
				slog.Info("route", "method", m, "pattern", route.Pattern)
			}
			continue
		}
		pattern := route.Pattern
		if len(pattern) > 0 && pattern[len(pattern)-1] == '/' {
			pattern += "*"
		}
		p.Mux.Handle(pattern, route.Handler)
		slog.Info("route", "pattern", route.Pattern)
	}

	slog.Info("──── registration complete ────", "app_routes", len(p.Routes))
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

			// 注册路由与健康检查
			routes := di.GetGroup[Route](i)
			RegisterRoutes(RouterParams{
				Mux:    mux,
				Routes: routes,
			})

			dbClient, _ := di.Invoke[*gormx.Client](i)
			RegisterHealth(HealthParams{
				Mux:    mux,
				Client: dbClient,
			})

			return mux, nil
		})
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
		// 触发初始化 listener
		_, _ = di.Invoke[*HTTPServer](i)
	},
)
