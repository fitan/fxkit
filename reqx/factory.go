package reqx

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/imroc/req/v3"
	"go.uber.org/fx"
)

const (
	defaultTimeout = 30 * time.Second

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
	BaseTransport http.RoundTripper

	// WatchWait is the Consul blocking query wait (default 5m, capped at 10m).
	// Longer waits do not delay change detection; Consul returns as soon as
	// the catalog index changes. Wait is only how long an idle query may hang.
	WatchWait time.Duration

	// MaxFailover is max distinct endpoints to try on failure (default 5).
	MaxFailover int
}

// NewFactory constructs a [Factory]. Prefer injecting via [Module].
func NewFactory(lc fx.Lifecycle, cfg *config.Config) *Factory {
	ctx, cancel := context.WithCancel(context.Background())
	f := &Factory{
		cfg:    cfg,
		pools:  make(map[string]*managedPool),
		root:   ctx,
		cancel: cancel,
	}
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			f.Close()
			return nil
		},
	})
	return f
}

// Close stops all Consul watches. Called automatically on Fx shutdown.
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

	rt := buildTransport(buildTransportInput{
		Pool:        pool,
		Scheme:      scheme,
		Name:        logicalName,
		Base:        in.BaseTransport,
		OTel:        otelOn,
		SpanName:    strings.TrimSpace(in.SpanName),
		MaxFailover: in.MaxFailover,
	})

	cli := req.C().
		SetTimeout(timeout).
		SetBaseURL(scheme + "://" + logicalName)

	httpClient := cli.GetClient()
	httpClient.Transport = rt
	httpClient.Timeout = timeout

	return cli, nil
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
		if eps, _, err := w.fetch(watchCtx, 0); err == nil && len(eps) > 0 {
			pool.replace(eps)
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
