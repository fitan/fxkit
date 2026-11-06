package fxkit_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/docs"
	fxhuma "github.com/fitan/fxkit/huma"
	"github.com/fitan/fxkit/server"
	"github.com/go-chi/chi/v5"
)

// registerProbe 注册一个返回固定 JSON 的 operation。
func registerProbe(api huma.API, dep *probeService) {
	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/probe",
	}, func(context.Context, *struct{}) (*probeOut, error) {
		return &probeOut{Body: map[string]string{"name": dep.Name}}, nil
	})
}

type probeOut struct {
	Body map[string]string
}

// writeConfig 写一个最小可启动的配置并返回路径。
//
// 这里不需要真实可用的端口：用 SkipListen 启动即可，写 "18080" 避免了
// 「先探测空闲端口再释放」的 TOCTOU 与对监听权限的依赖（CI/沙箱环境更稳）。
func writeConfig(t *testing.T, appName string) string {
	t.Helper()
	return writeConfigExtra(t, appName, "")
}

// writeConfigExtra 在最小配置后追加额外 YAML 段落。
func writeConfigExtra(t *testing.T, appName, extra string) string {
	t.Helper()
	return writeConfigWithPort(t, appName, "18080", extra)
}

// writeConfigWithPort 指定端口的配置（用于需要真实监听的测试）。
func writeConfigWithPort(t *testing.T, appName, port, extra string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "app:\n  name: " + appName + "\nserver:\n  port: \"" + port + "\"\ndb:\n  driver: \"\"\n"
	if extra != "" {
		body += extra
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRoutesFromModulesRegisteredAfterServer 是回归测试：
// 早期 ListenerModule 在装配期就把 *chi.Mux 固化，导致之后加入的模块
// （docs / mcpx / 业务模块）贡献的 server.Route 全部丢失。
func TestRoutesFromModulesRegisteredAfterServer(t *testing.T) {
	cfgPath := writeConfig(t, "route-order")

	root := di.New(
		func(i di.Injector) {
			di.ProvideValue(i, config.Options{ConfigFile: cfgPath})
			di.ProvideValue(i, &server.Runtime{SkipListen: true})
		},
		fxkit.Minimal(),
		fxhuma.Module,
		// 故意放在 Minimal() 之后，模拟「后装配的业务模块」
		server.ProvideRoutes(server.Route{
			Pattern: "/late",
			Methods: []string{http.MethodGet},
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("late-ok"))
			}),
		}),
	)
	defer root.Shutdown()

	lc, err := di.Invoke[*di.Lifecycle](root)
	if err != nil {
		t.Fatal(err)
	}
	if err := lc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = lc.Stop(context.Background()) }()

	mux, err := di.Invoke[*chi.Mux](root)
	if err != nil {
		t.Fatal(err)
	}

	// /docs 现在需要 docs.enabled=true（默认关闭），这里只断言路由与健康检查。
	for _, path := range []string{"/late", "/healthz"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s -> %d, want 200 (routes registered after server module were dropped)", path, rec.Code)
		}
	}
}

// TestServiceCtorIsRegistered 是回归测试：
// di.ToOption 早期对「有返回值」的构造函数静默丢弃，导致 fxkit.Service 形同虚设。
func TestServiceCtorIsRegistered(t *testing.T) {
	root := di.New()
	defer root.Shutdown()

	svcOption := fxkit.Service("probe", fxkit.Provide(func(di.Injector) (*probeService, error) {
		return &probeService{Name: "ok"}, nil
	}))
	svcOption(root)

	svc, err := di.Invoke[*probeService](root)
	if err != nil {
		t.Fatalf("service constructor was not registered: %v", err)
	}
	if svc.Name != "ok" {
		t.Fatalf("got %q", svc.Name)
	}
}

type probeService struct {
	Name string
}

// TestHostMiddlewareAppliedAfterDefault 是回归测试：
// 早期 consulx 在装配期 eager 解析 *server.HTTPServer，导致 *chi.Mux 在装配期就被
// 构建完成；之后（尤其在 Default() 之后）由宿主模块贡献的中间件全部丢失。
func TestHostMiddlewareAppliedAfterDefault(t *testing.T) {
	marker := server.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Probe", "1")
			next.ServeHTTP(w, r)
		})
	})

	root := di.New(fxkit.Default(), server.ProvideMiddleware(marker))
	defer di.Shutdown(root)

	lc, err := di.Invoke[*di.Lifecycle](root)
	if err != nil {
		t.Fatal(err)
	}
	if err := lc.StartPreStartOnly(context.Background()); err != nil {
		t.Fatalf("pre-start: %v", err)
	}

	mux, err := di.Invoke[*chi.Mux](root)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__probe_missing", nil))
	if got := rec.Header().Get("X-Probe"); got != "1" {
		t.Fatalf("host middleware contributed after Default() was dropped (X-Probe=%q)", got)
	}
}

// TestMinimalBindsListener 是回归测试：
// 早期 listener 的 bind hook 在 Lifecycle.Start 取完快照后才注册，导致 Minimal() 启动成功
// 却从不监听（Default() 因为 consulx 的 eager 解析而侥幸正常）。
func TestMinimalBindsListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind local port in this environment: %v", err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	body := "app:\n  name: listen-probe\nserver:\n  port: \"" + port + "\"\ndb:\n  driver: \"\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	root := di.New(
		func(i di.Injector) { di.ProvideValue(i, config.Options{ConfigFile: cfgPath}) },
		fxkit.Minimal(),
	)
	defer di.Shutdown(root)

	lc, err := di.Invoke[*di.Lifecycle](root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := lc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = lc.Stop(context.Background()) }()

	resp, err := http.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		t.Fatalf("listener did not bind (Minimal must listen): %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz -> %d", resp.StatusCode)
	}
}

// TestGracefulShutdownDrainsInFlight 验证 lc.Stop 会等在途请求完成，而不是直接切断。
func TestGracefulShutdownDrainsInFlight(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind local port in this environment: %v", err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	slow := server.Route{
		Pattern: "/slow",
		Methods: []string{http.MethodGet},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("done"))
		}),
	}

	cfgPath := writeConfigWithPort(t, "shutdown", port, "")
	root := di.New(
		func(i di.Injector) { di.ProvideValue(i, config.Options{ConfigFile: cfgPath}) },
		fxkit.Minimal(),
		server.ProvideRoutes(slow),
	)
	defer di.Shutdown(root)

	lc, err := di.Invoke[*di.Lifecycle](root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := lc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://127.0.0.1:" + port + "/slow")
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- lc.Stop(context.Background()) }()

	// 在途请求未结束时，Stop 不能返回。
	select {
	case err := <-stopDone:
		t.Fatalf("Stop returned before in-flight request finished: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return after handler finished")
	}

	select {
	case err := <-errCh:
		t.Fatalf("in-flight request failed: %v", err)
	case resp := <-respCh:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("in-flight request status = %d, want 200", resp.StatusCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request never completed")
	}
}

// TestDocsDisabledByDefault 是回归测试：/docs 默认不暴露，必须 docs.enabled=true。
func TestDocsDisabledByDefault(t *testing.T) {
	root := di.New(fxkit.Default())
	defer di.Shutdown(root)

	lc, err := di.Invoke[*di.Lifecycle](root)
	if err != nil {
		t.Fatal(err)
	}
	if err := lc.StartPreStartOnly(context.Background()); err != nil {
		t.Fatalf("pre-start: %v", err)
	}
	mux, err := di.Invoke[*chi.Mux](root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/docs", "/openapi.yaml", "/openapi.json"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s -> %d, want 404 (docs must be opt-in)", p, rec.Code)
		}
	}
}

// TestDocsOptIn 在 docs.enabled=true 时提供文档端点。
func TestDocsOptIn(t *testing.T) {
	cfgPath := writeConfigExtra(t, "docs-optin", "docs:\n  enabled: true\n")
	root := di.New(
		func(i di.Injector) {
			di.ProvideValue(i, config.Options{ConfigFile: cfgPath})
			di.ProvideValue(i, &server.Runtime{SkipListen: true})
		},
		fxkit.Minimal(),
		fxhuma.Module,
		docs.Module,
	)
	defer di.Shutdown(root)

	lc, err := di.Invoke[*di.Lifecycle](root)
	if err != nil {
		t.Fatal(err)
	}
	if err := lc.StartPreStartOnly(context.Background()); err != nil {
		t.Fatalf("pre-start: %v", err)
	}
	mux, err := di.Invoke[*chi.Mux](root)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /docs -> %d, want 200 when docs.enabled=true", rec.Code)
	}
}
func TestMCPDisabledByDefault(t *testing.T) {
	root := di.New(fxkit.Default())
	defer di.Shutdown(root)

	lc, err := di.Invoke[*di.Lifecycle](root)
	if err != nil {
		t.Fatal(err)
	}
	if err := lc.StartPreStartOnly(context.Background()); err != nil {
		t.Fatalf("pre-start: %v", err)
	}

	mux, err := di.Invoke[*chi.Mux](root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/mcp", "/mcp/sse", "/mcp/message"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s -> %d, want 404 (mcp.enabled defaults to false)", p, rec.Code)
		}
	}
}

// TestScaffoldedHumaRouteServed 验证 huma registrar 在完整装配后被安装，
// 且 /docs 能反映 live spec（PhasePostWire 的时序保证）。
func TestHumaRegistrarInstalledAfterAssembly(t *testing.T) {
	cfgPath := writeConfig(t, "huma-order")

	root := di.New(
		func(i di.Injector) {
			di.ProvideValue(i, config.Options{ConfigFile: cfgPath})
			di.ProvideValue(i, &server.Runtime{SkipListen: true})
		},
		fxkit.Minimal(),
		fxhuma.Module,
		// 依赖服务的 lazy registrar：延迟到 PreStart 求值
		fxhuma.ProvideRegistrarLazy(func(i di.Injector) (fxhuma.Registrar, error) {
			dep := di.MustInvoke[*probeService](i)
			return fxhuma.FuncRegistrar(func(api huma.API) {
				registerProbe(api, dep)
			}), nil
		}),
		di.Module("probe-service", func(i di.Injector) {
			di.ProvideValue(i, &probeService{Name: "served"})
		}),
	)
	defer root.Shutdown()

	lc, _ := di.Invoke[*di.Lifecycle](root)
	if err := lc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = lc.Stop(context.Background()) }()

	mux, _ := di.Invoke[*chi.Mux](root)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /probe -> %d, want 200 (registrar taking *Service was dropped)", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body["name"] != "served" {
		t.Fatalf("got %v", body)
	}
}
