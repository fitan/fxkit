package reqx

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/imroc/req/v3"
)

const (
	defaultTimeout = 30 * time.Second

	// defaultMaxIdleConnsPerHost avoids the stdlib global default of 2, which
	// causes connection churn for services that fan out to a few upstreams.
	defaultMaxIdleConnsPerHost = 32
	defaultMaxIdleConns        = 200
	defaultIdleConnTimeout     = 90 * time.Second

	// defaultWatchWait is Consul's blocking-query default. It is a max idle
	// hold: an index change returns immediately, so a longer wait does not
	// delay endpoint discovery.
	defaultWatchWait = 5 * time.Minute

	// maxWatchWait is Consul's blocking-query wait ceiling (10 minutes).
	maxWatchWait = 10 * time.Minute
)

// Factory builds [req.Client] instances that resolve targets via Consul watch
// (and/or static Seeds). Provided by [Module] (included in fxkit.Default).
type Factory struct {
	cfg *config.Config

	mu     sync.Mutex
	pools  map[string]*managedPool
	root   context.Context
	cancel context.CancelFunc
	// closed 标记已 Close。关闭后 root ctx 已取消，
	// 继续建带 Consul watch 的 client 会得到一个永不更新的空端点池。
	closed bool
}

type managedPool struct {
	pool   *endpointPool
	cancel context.CancelFunc
}

// ClientInput configures a discovered HTTP client.
type ClientInput struct {
	// Name is the Consul service name. Required when using Consul watch.
	// Endpoints are read from Consul health service entries (Service.Address or Node.Address + Port).
	Name string

	// Seeds are optional static endpoints for local/dev when Consul is unavailable, e.g.
	// "10.0.0.1:8080", "api.example.com:443". Production normally relies on Consul only.
	Seeds []string

	// Scheme is "http" (default) or "https".
	Scheme string

	// Timeout for the underlying http.Client (default 30s).
	Timeout time.Duration

	// PassingOnly selects only healthy Consul instances.
	// Default true. Set to a pointer to false to include non-passing.
	PassingOnly *bool

	// OTel enables otelhttp wrapping (default true).
	OTel *bool

	// SpanName overrides the otel span name formatter result (optional).
	SpanName string

	// BaseTransport is an optional underlying RoundTripper (before otel + resolve).
	//
	// When nil, the req.Client's own transport is used as the base, so
	// SetTLSClientConfig / SetProxy / SetDial / SetDialTLS etc. on the returned
	// client still affect real requests. When set, it replaces that transport
	// entirely; req-level transport settings are then ignored.
	BaseTransport http.RoundTripper

	// ServerName is the TLS server name (SNI + certificate verification) used
	// when Scheme is "https". Set it when discovery returns bare IPs but the
	// certificate is issued for a DNS name.
	//
	// When set, the logical service name is preserved as URL.Host / Host, and
	// each request dials the selected endpoint through a custom DialContext.
	// Without ServerName, https + a named service preserves the logical host
	// when discovery returns an IP endpoint, using it as the default SNI (so
	// Consul IP endpoints still verify). DNS endpoints keep the historical
	// behavior (endpoint used as SNI). Seeds-only clients also keep the
	// historical behavior.
	//
	// Host-preserving mode dials the selected endpoint directly, so an HTTP(S)
	// proxy configured on the client is bypassed for these requests.
	ServerName string

	// HostOverride overrides the HTTP Host header while still connecting to the
	// discovered endpoint. Useful for virtual-host routing through gateways.
	HostOverride string

	// WatchWait is the Consul blocking query wait (default 5m, capped at 10m).
	// Longer waits do not delay change detection; Consul returns as soon as
	// the catalog index changes. Wait is only how long an idle query may hang.
	WatchWait time.Duration

	// MaxFailover is max distinct endpoints to try on failure (default 5).
	MaxFailover int
}

// NewFactory constructs a [Factory]. Prefer injecting via [Module].
func NewFactory(lc *di.Lifecycle, cfg *config.Config) *Factory {
	ctx, cancel := context.WithCancel(context.Background())
	f := &Factory{
		cfg:    cfg,
		pools:  make(map[string]*managedPool),
		root:   ctx,
		cancel: cancel,
	}
	if lc != nil {
		lc.Append(di.Hook{
			OnStop: func(context.Context) error {
				f.Close()
				return nil
			},
		})
	}
	return f
}

// Close stops all Consul watches. Called automatically on Fx shutdown.
//
// 关闭后 Factory 不可再用于创建带 Consul watch 的 client —— 内部 root context
// 已被取消，新建的 watch 会立即退出（表现为「endpoints 永远不更新」）。
// [Client] 会显式拒绝这种用法而不是静默降级。
func (f *Factory) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
	for _, m := range f.pools {
		if m.cancel != nil {
			m.cancel()
		}
	}
	f.pools = make(map[string]*managedPool)
	f.closed = true
}

// IsClosed 报告 Factory 是否已关闭。
func (f *Factory) IsClosed() bool {
	if f == nil {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// Client returns a ready-to-use [req.Client]. Use relative paths; Host is filled
// from Consul healthy instances (round-robin + failover).
//
//	cli, _ := factory.Client(reqx.ClientInput{Name: "orders"})
//	cli.R().SetContext(ctx).Get("/v1/orders")
//
// HTTPS:
//
//	cli, _ := factory.Client(reqx.ClientInput{Name: "orders", Scheme: "https"})
//
// Transport customization: when ClientInput.BaseTransport is nil, the returned
// client's transport chain delegates to req's own transport, so
// SetTLSClientConfig / SetProxy / SetDial / SetDialTLS remain effective. In
// ServerName (host-preserving) mode the endpoint dialer wraps DialContext;
// calling SetDial afterwards replaces that wrapper, so configure custom dialers
// through BaseTransport instead.
func (f *Factory) Client(in ClientInput) (*req.Client, error) {
	if f == nil {
		return nil, fmt.Errorf("reqx: nil factory")
	}
	name := strings.TrimSpace(in.Name)
	seeds := normalizeEndpoints(in.Seeds)
	if name == "" && len(seeds) == 0 {
		return nil, fmt.Errorf("reqx: ClientInput.Name or Seeds required")
	}

	scheme := strings.ToLower(strings.TrimSpace(in.Scheme))
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("reqx: unsupported scheme %q (want http or https)", in.Scheme)
	}

	timeout := in.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	otelOn := true
	if in.OTel != nil {
		otelOn = *in.OTel
	}

	pool, err := f.ensurePool(ensurePoolInput{
		Name:        name,
		Seeds:       seeds,
		PassingOnly: in.PassingOnly,
		WatchWait:   in.WatchWait,
	})
	if err != nil {
		return nil, err
	}

	logicalName := name
	if logicalName == "" {
		logicalName = "static"
	}

	serverName := normalizeServerName(in.ServerName)
	hostOverride := strings.TrimSpace(in.HostOverride)
	explicitServerName := serverName != ""
	// Preserve the logical host when an explicit TLS name is requested, or for
	// https + Consul-discovered named services (endpoint may be a bare IP).
	preserveHost := explicitServerName || (scheme == "https" && name != "")
	// In the inferred case only IP endpoints need host preservation; DNS
	// endpoints keep the historical behavior (endpoint used as SNI).
	autoPreserve := preserveHost && !explicitServerName

	cli := req.C().
		SetTimeout(timeout).
		SetBaseURL(scheme + "://" + logicalName)

	base := in.BaseTransport
	ownBase := false
	if base == nil {
		// Chain req's own transport: SetTLSClientConfig / SetProxy / SetDial
		// mutate cli.Transport, which must be on the request path.
		reqTransport := cli.GetTransport()
		tuneReqTransport(reqTransport)
		base = reqTransport
		ownBase = true
	}

	rt, err := buildTransport(buildTransportInput{
		Pool:         pool,
		Scheme:       scheme,
		Name:         logicalName,
		LogicalHost:  logicalName,
		Base:         base,
		OwnBase:      ownBase,
		OTel:         otelOn,
		SpanName:     strings.TrimSpace(in.SpanName),
		MaxFailover:  in.MaxFailover,
		PreserveHost: preserveHost,
		AutoPreserve: autoPreserve,
		ServerName:   serverName,
		HostOverride: hostOverride,
	})
	if err != nil {
		return nil, err
	}

	httpClient := cli.GetClient()
	httpClient.Transport = rt
	httpClient.Timeout = timeout

	return cli, nil
}

// tuneReqTransport adjusts req's per-client transport defaults.
//
// req.C() creates a fresh transport per client (never the global
// http.DefaultTransport), so tuning it here avoids sharing connection-pool
// state across clients while keeping sane keep-alive settings.
func tuneReqTransport(t *req.Transport) {
	if t == nil {
		return
	}
	if t.MaxIdleConnsPerHost <= 0 {
		t.MaxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	}
	if t.MaxIdleConns <= 0 {
		t.MaxIdleConns = defaultMaxIdleConns
	}
	if t.IdleConnTimeout <= 0 {
		t.IdleConnTimeout = defaultIdleConnTimeout
	}
}

// normalizeServerName strips an accidental scheme/port so tls.Config.ServerName
// always receives a bare host name.
func normalizeServerName(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return strings.TrimRight(s, "/")
}

// TransportClient returns the stdlib [http.Client] whose Transport resolves
// Consul endpoints (failover + OTel) and the logical server URL
// (`scheme://service-name`, or `scheme://static` when only Seeds are set).
//
// Use with OpenAPI-generated clients (oapi-codegen `WithHTTPClient`):
//
//	httpClient, server, err := factory.TransportClient(reqx.ClientInput{Name: "users"})
//	sdk, err := users.NewClientWithResponses(server, users.WithHTTPClient(httpClient))
func (f *Factory) TransportClient(in ClientInput) (*http.Client, string, error) {
	cli, err := f.Client(in)
	if err != nil {
		return nil, "", err
	}
	scheme := strings.ToLower(strings.TrimSpace(in.Scheme))
	if scheme == "" {
		scheme = "http"
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "static"
	}
	return cli.GetClient(), scheme + "://" + name, nil
}

type ensurePoolInput struct {
	Name        string
	Seeds       []string
	PassingOnly *bool
	WatchWait   time.Duration
}

func (f *Factory) ensurePool(in ensurePoolInput) (*endpointPool, error) {
	passingOnly, consulAddr, err := discoverySettings(in.PassingOnly, f.cfg)
	if err != nil {
		return nil, err
	}

	key := poolKey(in.Name, passingOnly, in.Seeds)

	f.mu.Lock()
	defer f.mu.Unlock()

	// 关闭后 root ctx 已取消，新建 watch 会立刻退出 —— 静默返回一个永不更新的
	// 端点池比直接报错更难排查（业务表现为「一直连不上服务」）。
	if f.closed {
		return nil, fmt.Errorf("reqx: factory is closed; create a new one (Close cancels all Consul watches)")
	}

	if m, ok := f.pools[key]; ok {
		return m.pool, nil
	}

	pool := newEndpointPool(in.Seeds)
	watchCtx, watchCancel := context.WithCancel(f.root)

	if in.Name != "" && consulAddr != "" {
		base, err := normalizeConsulBase(consulAddr)
		if err != nil {
			watchCancel()
			return nil, fmt.Errorf("reqx: consul address: %w", err)
		}
		w := watchConfig{
			consulBase:  base,
			service:     in.Name,
			passingOnly: passingOnly,
			wait:        in.WatchWait,
			httpClient:  &http.Client{Timeout: 10 * time.Second},
		}
		if eps, _, err := w.fetch(watchCtx, 0); err != nil {
			// Discovery failing at startup used to be silent: the client was
			// created with an empty pool and requests failed with a confusing
			// "no healthy endpoints". Log it loudly; the watch below keeps
			// retrying so a temporary Consul outage still recovers.
			slog.Warn("reqx: initial service discovery failed",
				"service", in.Name,
				"error", err,
			)
		} else if len(eps) > 0 {
			pool.replace(eps)
		} else if len(in.Seeds) == 0 {
			slog.Warn("reqx: initial service discovery returned no endpoints",
				"service", in.Name,
			)
		}

		go watchConfig{
			consulBase:  base,
			service:     in.Name,
			passingOnly: passingOnly,
			wait:        in.WatchWait,
		}.run(watchCtx, pool)
	} else if in.Name != "" && consulAddr == "" && len(in.Seeds) == 0 {
		watchCancel()
		return nil, fmt.Errorf("reqx: discovery.consul_address empty and no Seeds for service %q", in.Name)
	}

	f.pools[key] = &managedPool{pool: pool, cancel: watchCancel}
	return pool, nil
}

func discoverySettings(override *bool, cfg *config.Config) (passingOnly bool, consulAddr string, err error) {
	passingOnly = true
	if cfg != nil {
		var dc *discoveryConfig
		dc, err = config.Load[discoveryConfig](cfg, "discovery")
		if err != nil {
			return false, "", err
		}
		passingOnly = dc.ConsulPassingOnly
		consulAddr = strings.TrimSpace(dc.ConsulAddress)
	}
	if override != nil {
		passingOnly = *override
	}
	return passingOnly, consulAddr, nil
}

func poolKey(name string, passingOnly bool, seeds []string) string {
	b := strings.Builder{}
	b.WriteString(strings.ToLower(strings.TrimSpace(name)))
	b.WriteByte('|')
	if passingOnly {
		b.WriteByte('1')
	} else {
		b.WriteByte('0')
	}
	b.WriteByte('|')
	b.WriteString(strings.Join(seeds, ","))
	return b.String()
}

// Endpoints returns the current discovered targets for a previously created client key.
func (f *Factory) Endpoints(in ClientInput) []string {
	if f == nil {
		return nil
	}
	passingOnly, _, err := discoverySettings(in.PassingOnly, f.cfg)
	if err != nil {
		return nil
	}
	key := poolKey(strings.TrimSpace(in.Name), passingOnly, normalizeEndpoints(in.Seeds))
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.pools[key]
	if !ok {
		return nil
	}
	return m.pool.snapshot()
}
