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

	// defaultMaxIdleConnsPerHost 避免使用标准库全局默认值 2，
	// 该默认值会让扇出到少量上游的服务频繁重建连接。
	defaultMaxIdleConnsPerHost = 32
	defaultMaxIdleConns        = 200
	defaultIdleConnTimeout     = 90 * time.Second

	// defaultWatchWait 是 Consul blocking query 的默认值。它是最大空闲
	// 挂起时间：index 变化会立即返回，因此更长的 wait 不会延迟
	// 端点发现。
	defaultWatchWait = 5 * time.Minute

	// maxWatchWait 是 Consul blocking query 的 wait 上限（10 分钟）。
	maxWatchWait = 10 * time.Minute
)

// Factory 构建通过 Consul 监听（和/或静态种子地址）解析目标的 [req.Client] 实例。
// 由 [Module] 提供（已包含在 fxkit.Default 中）。
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

// ClientInput 配置一个带服务发现的 HTTP 客户端。
type ClientInput struct {
	// Name 是 Consul 服务名。使用 Consul 监听时必填。
	// 端点从 Consul 健康服务条目读取（Service.Address 或 Node.Address + Port）。
	Name string

	// Seeds 是可选的静态端点，用于 Consul 不可用的本地/开发环境，例如
	// "10.0.0.1:8080"、"api.example.com:443"。生产环境通常只依赖 Consul。
	Seeds []string

	// Scheme 为 "http"（默认）或 "https"。
	Scheme string

	// Timeout 是底层 http.Client 的超时时间（默认 30s）。
	Timeout time.Duration

	// PassingOnly 只选择健康的 Consul 实例。
	// 默认为 true。设为指向 false 的指针可包含非 passing 实例。
	PassingOnly *bool

	// OTel 启用 otelhttp 包装（默认 true）。
	OTel *bool

	// SpanName 覆盖 otel span 名称格式化器的结果（可选）。
	SpanName string

	// BaseTransport 是可选的底层 RoundTripper（位于 otel 与解析层之前）。
	//
	// 为 nil 时，使用 req.Client 自身的传输层作为 base，因此在返回的 client 上
	// 调用 SetTLSClientConfig / SetProxy / SetDial / SetDialTLS 等仍会影响真实请求。
	// 设置后则完全替换该传输层；此时 req 层面的传输层设置会被忽略。
	BaseTransport http.RoundTripper

	// ServerName 是 TLS 服务器名（用于 SNI 与证书校验），在 Scheme 为 "https" 时使用。
	// 当服务发现返回裸 IP、而证书签发对象是 DNS 名时设置它。
	//
	// 设置后，逻辑服务名会保留在 URL.Host / Host 中，每个请求通过自定义
	// DialContext 拨号到选中的端点。未设置 ServerName 时，https + 命名服务在
	// 服务发现返回 IP 端点的情况下会保留逻辑 host，并将其作为默认 SNI
	// （因此 Consul 返回的 IP 端点仍能通过校验）。DNS 端点保持原有行为
	// （端点即 SNI）。仅使用种子地址的 client 也保持原有行为。
	//
	// 保留 host 模式会直接拨号到选中的端点，因此 client 上配置的 HTTP(S)
	// 代理对这类请求会被绕过。
	ServerName string

	// HostOverride 覆盖 HTTP Host 头，同时仍连接到服务发现的端点。
	// 适用于通过网关进行虚拟主机路由的场景。
	HostOverride string

	// WatchWait 是 Consul blocking query 的 wait（默认 5m，上限 10m）。
	// 更长的 wait 不会延迟变更检测；catalog index 一变化 Consul 就会返回。
	// wait 只是空闲查询最多可以挂起多久。
	WatchWait time.Duration

	// MaxFailover 是失败时最多尝试的不同端点数量（默认 5）。
	MaxFailover int
}

// NewFactory 构造 [Factory]。优先通过 [Module] 注入。
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

// Close 停止所有 Consul 监听。Fx 关闭时会自动调用。
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

// Client 返回一个开箱即用的 [req.Client]。使用相对路径；Host 由 Consul 健康实例填充
// （round-robin + 故障转移）。
//
//	cli, _ := factory.Client(reqx.ClientInput{Name: "orders"})
//	cli.R().SetContext(ctx).Get("/v1/orders")
//
// HTTPS：
//
//	cli, _ := factory.Client(reqx.ClientInput{Name: "orders", Scheme: "https"})
//
// 传输层定制：当 ClientInput.BaseTransport 为 nil 时，返回 client 的传输层链会委托给
// req 自身的传输层，因此 SetTLSClientConfig / SetProxy / SetDial / SetDialTLS 仍然有效。
// 在 ServerName（保留 host）模式下，端点拨号器会包装 DialContext；之后再调用 SetDial
// 会替换掉该包装，因此请通过 BaseTransport 配置自定义拨号器。
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
	// 显式要求 TLS 名称时保留逻辑 host，或对 https + Consul 发现的命名服务
	// 保留（端点可能是裸 IP）。
	preserveHost := explicitServerName || (scheme == "https" && name != "")
	// 在推断场景下只有 IP 端点需要保留 host；DNS 端点保持原有行为
	// （端点即 SNI）。
	autoPreserve := preserveHost && !explicitServerName

	cli := req.C().
		SetTimeout(timeout).
		SetBaseURL(scheme + "://" + logicalName)

	base := in.BaseTransport
	ownBase := false
	if base == nil {
		// 串联 req 自身的传输层：SetTLSClientConfig / SetProxy / SetDial
		// 会修改 cli.Transport，它必须位于请求路径上。
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

// tuneReqTransport 调整 req 的每 client 传输层默认值。
//
// req.C() 会为每个 client 创建新的传输层（从不使用全局的
// http.DefaultTransport），因此在这里调整可以避免在 client 之间共享
// 连接池状态，同时保留合理的 keep-alive 设置。
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

// normalizeServerName 去掉误加上的 scheme/port，确保 tls.Config.ServerName
// 总能收到裸 host 名。
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

// TransportClient 返回标准库 [http.Client]，其 Transport 会解析
// Consul 端点（故障转移 + OTel），并返回逻辑服务 URL
// （`scheme://service-name`，仅设置 Seeds 时为 `scheme://static`）。
//
// 用于 OpenAPI 生成的客户端（oapi-codegen `WithHTTPClient`）：
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
			// 启动时服务发现失败过去是静默的：client 会带着空端点池创建，
			// 请求以令人困惑的 "no healthy endpoints" 失败。这里要大声记录；
			// 下面的监听会持续重试，因此临时性的 Consul 故障仍可恢复。
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

// Endpoints 返回此前创建的 client key 当前发现的目标。
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
