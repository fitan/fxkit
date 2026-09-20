package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/otelx"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
)

// MuxParams 是 [NewMux] 的输入。任意模块可向 "middleware" fx group 贡献 [Middleware] 以扩展 chi mux。
type MuxParams struct {
	fx.In
	Server           *Config
	Otel             *otelx.Config `optional:"true"`
	ExtraMiddlewares []Middleware  `group:"middleware"`
}

// NewMux 构建共享 chi mux，默认中间件链为 CORS → OTel HTTP（启用时）→ route-pattern → recover，
// 并加上其他模块贡献的中间件。otelhttp 最多在此处安装一次。
//
// server.log_payloads 为 true 时启用请求体日志。
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
	fx.In
	Mux    *chi.Mux
	Routes []Route `group:"routes"`
}

// RegisterRoutes 在共享 chi mux 上挂载 grouped [Route] 值。
// 存活/就绪探针通过 [RegisterHealth] 注册。
func RegisterRoutes(p RouterParams) {
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

// Runtime 控制 HTTP listener 行为。通过 fx.Supply 注入；缺省则正常监听。
// `openapi` 子命令会 Supply SkipListen=true，装配同一张 Fx 图但不绑端口。
type Runtime struct {
	SkipListen bool
}

// ServerParams 列出 [NewHTTPServer] 的输入。
type ServerParams struct {
	fx.In
	Lifecycle fx.Lifecycle
	Server    *Config
	Mux       *chi.Mux
	Runtime   *Runtime `optional:"true"`
}

// HTTPServer 是 [http.Server] 的薄封装，供需要显式引用绑定 server 的 Fx 图使用（例如测试或需要地址的模块）。
type HTTPServer struct {
	*http.Server
}

// NewHTTPServer 将 chi mux 绑定到支持 h2c 的 [http.Server]（配置端口），并通过 Fx 生命周期 hook 管理 Start/Stop。
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

	p.Lifecycle.Append(fx.Hook{
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

	return &HTTPServer{Server: srv}
}

// Module 提供共享 chi mux 与最终 router，供需挂载路由的模块使用。
// 不包含 HTTP listener —— 独立 HTTP 请配合 [ListenerModule]。
var Module = fx.Module("fxkit/server",
	config.Provide[Config]("server"),
	fx.Provide(
		fx.Annotate(defaultRoutes, fx.ResultTags(`group:"routes,flatten"`)),
		fx.Annotate(defaultMiddleware, fx.ResultTags(`group:"middleware"`)),
		NewMux,
	),
	fx.Invoke(RegisterRoutes, RegisterHealth),
)

// ListenerModule 在共享 mux 上启动独立 HTTP listener。
var ListenerModule = fx.Module("fxkit/server/listener",
	fx.Provide(NewHTTPServer),
	fx.Invoke(func(*HTTPServer) {}),
)
