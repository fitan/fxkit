package reqx

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/imroc/req/v3"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
)

const (
	defaultMaxFailover       = 5
	defaultMaxRewindBodySize = 4 * 1024 * 1024 // 缓冲请求体的上限 4MB
)

// resolveTransport 选择一个已发现的端点并改写请求 host，
// 然后委托给下一个 RoundTripper（通常是 otelhttp → 基础传输层）。
// 遇到传输层失败或选中的 5xx 时，会尝试另一个端点，对任意方法都适用。
//
// 默认情况下请求的 URL.Host/Host 会被改写为选中的端点（历史行为）。
// 当设置了 preserveHost（配置了 ServerName，或 https + 命名的 Consul
// 服务）时，逻辑服务名保留在 URL.Host 中，选中的端点改为注入到拨号
// context 里，这样即使 Consul 返回裸 IP，TLS SNI / 证书校验也能使用
// 服务 DNS 名。参见 [ClientInput.ServerName]。
type resolveTransport struct {
	pool        *endpointPool
	scheme      string
	name        string
	maxFailover int
	next        http.RoundTripper

	preserveHost     bool
	autoPreserveHost bool
	logicalHost      string
	hostOverride     string
}

func (t *resolveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := ensureRewindableBody(req); err != nil {
		return nil, fmt.Errorf("reqx(%s): rewind body: %w", t.name, err)
	}
	n := t.pool.len()
	if n == 0 {
		return nil, fmt.Errorf("reqx(%s): no healthy endpoints", t.name)
	}

	attempts := n
	if t.maxFailover > 0 && t.maxFailover < attempts {
		attempts = t.maxFailover
	}
	if attempts > defaultMaxFailover {
		attempts = defaultMaxFailover
	}

	scheme := t.scheme
	if scheme == "" {
		scheme = "http"
	}

	var lastErr error
	tried := make(map[string]struct{}, attempts)
	hasUnrewindableBody := req.Body != nil && req.Body != http.NoBody && req.GetBody == nil

	for i := 0; i < attempts; i++ {
		ep, err := t.pool.next()
		if err != nil {
			return nil, fmt.Errorf("reqx(%s): %w", t.name, err)
		}
		if _, ok := tried[ep]; ok {
			// 已尝试过该 host；继续轮换直到出现新端点或耗尽。
			if len(tried) >= n {
				break
			}
			i--
			continue
		}
		tried[ep] = struct{}{}

		r2, err := cloneForEndpoint(req, ep, endpointRewrite{
			scheme:       scheme,
			preserveHost: t.shouldPreserveHost(ep),
			logicalHost:  t.logicalHost,
			hostOverride: t.hostOverride,
		})
		if err != nil {
			return nil, fmt.Errorf("reqx(%s): %w", t.name, err)
		}

		resp, err := t.next.RoundTrip(r2)
		if err == nil {
			if shouldFailoverStatus(resp.StatusCode) && i+1 < attempts && len(tried) < n {
				if hasUnrewindableBody {
					// 无法安全回卷大体积或流式 body；不要用空 body 重试
					return resp, nil
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				lastErr = fmt.Errorf("reqx(%s): endpoint %s status %d", t.name, ep, resp.StatusCode)
				continue
			}
			return resp, nil
		}

		lastErr = err
		if !isFailoverError(err) || i+1 >= attempts || hasUnrewindableBody {
			return nil, fmt.Errorf("reqx(%s): endpoint %s: %w", t.name, ep, err)
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("reqx(%s): no healthy endpoints", t.name)
	}
	return nil, lastErr
}

// shouldPreserveHost 报告该端点是否应保留逻辑 host。自动模式
// （https + 命名服务，且未显式设置 ServerName）仅在 Consul 返回裸 IP
// 时才需要保留；DNS 端点保持原有的「端点即 SNI」行为。
func (t *resolveTransport) shouldPreserveHost(ep string) bool {
	if !t.preserveHost {
		return false
	}
	if !t.autoPreserveHost {
		return true
	}
	return isIPEndpoint(ep)
}

func endpointHost(ep string) string {
	if host, _, err := net.SplitHostPort(ep); err == nil {
		return host
	}
	return strings.Trim(strings.TrimSpace(ep), "[]")
}

func isIPEndpoint(ep string) bool {
	return net.ParseIP(endpointHost(ep)) != nil
}

type endpointRewrite struct {
	scheme       string
	preserveHost bool
	logicalHost  string
	hostOverride string
}

func cloneForEndpoint(req *http.Request, ep string, o endpointRewrite) (*http.Request, error) {
	if req.URL == nil {
		return nil, fmt.Errorf("nil request URL")
	}
	r2 := req.Clone(req.Context())

	host := ep
	if o.preserveHost {
		host = o.logicalHost
		if host == "" {
			host = req.URL.Host
		}
		// 实际 TCP 目标通过 context 传递，由自定义 DialContext 消费
		// （见 prepareEndpointDialer）。
		r2 = r2.WithContext(withDialEndpoint(r2.Context(), ep))
	}

	r2.URL.Scheme = o.scheme
	r2.URL.Host = host
	if o.hostOverride != "" {
		r2.Host = o.hostOverride
	} else {
		r2.Host = host
	}

	// 为故障转移重试回卷 body。
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r2.Body = body
	}
	return r2, nil
}

type dialEndpointKey struct{}

func withDialEndpoint(ctx context.Context, ep string) context.Context {
	return context.WithValue(ctx, dialEndpointKey{}, ep)
}

func dialEndpointFromContext(ctx context.Context) (string, bool) {
	ep, ok := ctx.Value(dialEndpointKey{}).(string)
	if !ok || ep == "" {
		return "", false
	}
	return ep, true
}

// prepareEndpointDialer 向基础传输层注入能感知端点的 DialContext，
// 以便 URL.Host 可以保持为逻辑服务名。
//
// 支持的基础传输层为 *http.Transport 与 req 的 *req.Transport。其他
// RoundTripper 无法在拨号层被拦截，因此保留 host 模式会返回明确错误，
// 而不是静默连接到错误的 host。
func prepareEndpointDialer(rt http.RoundTripper, serverName string, own bool) (http.RoundTripper, error) {
	switch t := rt.(type) {
	case *http.Transport:
		// 始终克隆：BaseTransport 可能被共享（例如 httptest 的 client）。
		c := t.Clone()
		c.DialContext = wrapEndpointDialer(c.DialContext)
		if c.DialTLSContext != nil {
			if serverName != "" {
				return nil, fmt.Errorf("reqx: ServerName cannot be applied when BaseTransport has DialTLSContext set")
			}
			c.DialTLSContext = wrapEndpointDialer(c.DialTLSContext)
		}
		if serverName != "" {
			c.TLSClientConfig = cloneTLSConfig(c.TLSClientConfig)
			c.TLSClientConfig.ServerName = serverName
		}
		return c, nil

	case *req.Transport:
		c := t
		if !own {
			c = t.Clone()
		}
		c.DialContext = wrapEndpointDialer(c.DialContext)
		if c.DialTLSContext != nil {
			if serverName != "" {
				return nil, fmt.Errorf("reqx: ServerName cannot be applied when BaseTransport has DialTLSContext set")
			}
			c.DialTLSContext = wrapEndpointDialer(c.DialTLSContext)
		}
		if serverName != "" {
			c.TLSClientConfig = cloneTLSConfig(c.TLSClientConfig)
			c.TLSClientConfig.ServerName = serverName
		}
		return c, nil

	default:
		return nil, fmt.Errorf("reqx: ServerName/host-preserving mode requires *http.Transport or *req.Transport, got %T", rt)
	}
}

func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{}
	}
	return cfg.Clone()
}

func wrapEndpointDialer(orig func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if orig == nil {
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		orig = d.DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if ep, ok := dialEndpointFromContext(ctx); ok {
			addr = ep
		}
		return orig(ctx, network, addr)
	}
}

func ensureRewindableBody(req *http.Request) error {
	return rewindBody(req, defaultMaxRewindBodySize)
}

func rewindBody(req *http.Request, max int64) error {
	if req == nil || req.GetBody != nil || req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if max <= 0 {
		return nil
	}
	if req.ContentLength > max {
		// body 超过回卷缓冲区上限；保持流原样且不设置 GetBody，避免 OOM。
		return nil
	}

	orig := req.Body
	buf, err := io.ReadAll(io.LimitReader(orig, max+1))
	if err != nil {
		_ = orig.Close()
		return err
	}
	if int64(len(buf)) > max {
		// 分块或未知长度超过了缓冲区。保留原始 closer，以便剩余内容
		// 仍可读取；不要设置 GetBody。
		req.Body = &readCloser{Reader: io.MultiReader(bytes.NewReader(buf), orig), Closer: orig}
		return nil
	}
	_ = orig.Close()

	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf)), nil
	}
	req.Body, err = req.GetBody()
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(buf))
	return nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

func shouldFailoverStatus(code int) bool {
	switch code {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isFailoverError(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "TLS handshake timeout") ||
		strings.Contains(msg, "EOF")
}

func buildTransport(in buildTransportInput) (http.RoundTripper, error) {
	base := in.Base
	if base == nil {
		base = cloneDefaultTransport()
	}

	if in.PreserveHost {
		prepared, err := prepareEndpointDialer(base, in.ServerName, in.OwnBase)
		if err != nil {
			return nil, err
		}
		base = prepared
	}

	next := base
	if in.OTel {
		opts := []otelhttp.Option{
			otelhttp.WithTracerProvider(otel.GetTracerProvider()),
			otelhttp.WithPropagators(otel.GetTextMapPropagator()),
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				if in.SpanName != "" {
					return in.SpanName
				}
				return "HTTP " + r.Method
			}),
		}
		next = otelhttp.NewTransport(base, opts...)
	}

	maxFailover := in.MaxFailover
	if maxFailover <= 0 {
		maxFailover = defaultMaxFailover
	}

	return &resolveTransport{
		pool:             in.Pool,
		scheme:           in.Scheme,
		name:             in.Name,
		maxFailover:      maxFailover,
		next:             next,
		preserveHost:     in.PreserveHost,
		autoPreserveHost: in.AutoPreserve,
		logicalHost:      in.LogicalHost,
		hostOverride:     in.HostOverride,
	}, nil
}

func cloneDefaultTransport() http.RoundTripper {
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		c := dt.Clone()
		tuneStdTransport(c)
		return c
	}
	return http.DefaultTransport
}

func tuneStdTransport(t *http.Transport) {
	if t == nil {
		return
	}
	// 绝不继承全局传输层的 MaxIdleConnsPerHost=2 默认值。
	if t.MaxIdleConnsPerHost <= 0 {
		t.MaxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	}
	if t.MaxIdleConns < defaultMaxIdleConns {
		t.MaxIdleConns = defaultMaxIdleConns
	}
	if t.IdleConnTimeout <= 0 {
		t.IdleConnTimeout = defaultIdleConnTimeout
	}
}

type buildTransportInput struct {
	Pool         *endpointPool
	Scheme       string
	Name         string
	LogicalHost  string
	Base         http.RoundTripper
	OwnBase      bool
	OTel         bool
	SpanName     string
	MaxFailover  int
	PreserveHost bool
	// AutoPreserve 表示 PreserveHost 是推断出来的（https + 命名服务，
	// 未显式设置 ServerName），应只对 IP 端点生效。
	AutoPreserve bool
	ServerName   string
	HostOverride string
}
