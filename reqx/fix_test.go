package reqx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureTransport struct {
	req    *http.Request
	dialEP string
	status int
}

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.req = r
	c.dialEP, _ = dialEndpointFromContext(r.Context())
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    r,
	}, nil
}

func TestCloneForEndpoint_PreserveLogicalHost(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://orders/v1/items", nil)
	require.NoError(t, err)

	r2, err := cloneForEndpoint(req, "10.1.2.3:8443", endpointRewrite{
		scheme:       "https",
		preserveHost: true,
		logicalHost:  "orders",
	})
	require.NoError(t, err)

	assert.Equal(t, "orders", r2.URL.Host)
	assert.Equal(t, "orders", r2.Host)
	ep, ok := dialEndpointFromContext(r2.Context())
	assert.True(t, ok, "the selected endpoint must travel in the dial context")
	assert.Equal(t, "10.1.2.3:8443", ep)
}

func TestCloneForEndpoint_HostOverride(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://orders/v1", nil)
	require.NoError(t, err)

	// 改写模式 + Host 覆盖：连接指向端点，但 Host 头保持为
	// 便于虚拟主机路由的名称。
	r2, err := cloneForEndpoint(req, "10.1.2.3:8080", endpointRewrite{
		scheme:       "http",
		hostOverride: "api.internal",
	})
	require.NoError(t, err)
	assert.Equal(t, "10.1.2.3:8080", r2.URL.Host)
	assert.Equal(t, "api.internal", r2.Host)

	// 保留模式 + Host 覆盖。
	r3, err := cloneForEndpoint(req, "10.1.2.3:8443", endpointRewrite{
		scheme:       "https",
		preserveHost: true,
		logicalHost:  "orders",
		hostOverride: "api.internal",
	})
	require.NoError(t, err)
	assert.Equal(t, "orders", r3.URL.Host)
	assert.Equal(t, "api.internal", r3.Host)
}

func TestCloneForEndpoint_DefaultRewriteKeepsOldBehavior(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://orders/v1", nil)
	require.NoError(t, err)

	r2, err := cloneForEndpoint(req, "10.1.2.3:8080", endpointRewrite{scheme: "http"})
	require.NoError(t, err)
	assert.Equal(t, "10.1.2.3:8080", r2.URL.Host)
	assert.Equal(t, "10.1.2.3:8080", r2.Host)
	_, ok := dialEndpointFromContext(r2.Context())
	assert.False(t, ok, "default mode must not inject a dial endpoint")
}

func TestResolveTransport_PreserveHostDialTarget(t *testing.T) {
	capture := &captureTransport{}
	rt := &resolveTransport{
		pool:         newEndpointPool([]string{"10.1.2.3:8443"}),
		scheme:       "https",
		name:         "orders",
		maxFailover:  1,
		next:         capture,
		preserveHost: true,
		logicalHost:  "orders",
	}
	req, err := http.NewRequest(http.MethodGet, "https://orders/v1", nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, "orders", capture.req.URL.Host, "URL.Host keeps the logical service name")
	assert.Equal(t, "orders", capture.req.Host)
	assert.Equal(t, "10.1.2.3:8443", capture.dialEP, "DialContext receives the discovered endpoint")
}

func TestWrapEndpointDialer_UsesInjectedEndpoint(t *testing.T) {
	var got string
	orig := func(_ context.Context, _, addr string) (net.Conn, error) {
		got = addr
		return nil, errors.New("stop")
	}
	dial := wrapEndpointDialer(orig)

	_, _ = dial(withDialEndpoint(context.Background(), "10.1.2.3:8443"), "tcp", "orders:443")
	assert.Equal(t, "10.1.2.3:8443", got)

	_, _ = dial(context.Background(), "tcp", "orders:443")
	assert.Equal(t, "orders:443", got, "without a selected endpoint the original address is used")
}

func TestPrepareEndpointDialer_HTTPServerName(t *testing.T) {
	base := &http.Transport{}
	var got string
	base.DialContext = func(_ context.Context, _, addr string) (net.Conn, error) {
		got = addr
		return nil, errors.New("stop")
	}

	prepared, err := prepareEndpointDialer(base, "api.example.com", false)
	require.NoError(t, err)
	httpRT, ok := prepared.(*http.Transport)
	require.True(t, ok)
	assert.NotSame(t, base, httpRT, "base transport must be cloned")
	require.NotNil(t, httpRT.TLSClientConfig)
	assert.Equal(t, "api.example.com", httpRT.TLSClientConfig.ServerName, "SNI / cert verification name")

	_, _ = httpRT.DialContext(withDialEndpoint(context.Background(), "10.1.2.3:8443"), "tcp", "orders:443")
	assert.Equal(t, "10.1.2.3:8443", got)

	// 原 transport 不被修改。
	assert.Nil(t, base.TLSClientConfig)
}

func TestPrepareEndpointDialer_RejectsUnknownTransport(t *testing.T) {
	custom := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("not used")
	})
	_, err := prepareEndpointDialer(custom, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires *http.Transport or *req.Transport")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNormalizeServerName(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"api.example.com":               "api.example.com",
		"api.example.com:443":           "api.example.com",
		"https://api.example.com/":      "api.example.com",
		"https://api.example.com:8443/": "api.example.com",
		"  orders.internal  ":           "orders.internal",
	}
	for in, want := range cases {
		assert.Equal(t, want, normalizeServerName(in), "input=%q", in)
	}
}

func TestClient_PreservesHostForNamedHTTPS(t *testing.T) {
	f := NewFactory(nil, nil)
	defer f.Close()
	off := false

	cli, err := f.Client(ClientInput{
		Name:   "orders",
		Seeds:  []string{"10.1.2.3:8443"},
		Scheme: "https",
		OTel:   &off,
	})
	require.NoError(t, err)

	rt, ok := cli.GetClient().Transport.(*resolveTransport)
	require.True(t, ok)
	assert.True(t, rt.preserveHost, "https + named service must preserve the logical host")
	assert.True(t, rt.autoPreserveHost, "no explicit ServerName => inferred (IP-only) mode")
	assert.Equal(t, "orders", rt.logicalHost)
	assert.Same(t, cli.GetTransport(), rt.next, "resolveTransport must chain req's own transport")
	assert.Equal(t, defaultMaxIdleConnsPerHost, cli.GetTransport().MaxIdleConnsPerHost)
}

func TestClient_ServerNameAppliedToReqTransport(t *testing.T) {
	f := NewFactory(nil, nil)
	defer f.Close()
	off := false

	cli, err := f.Client(ClientInput{
		Name:       "orders",
		Seeds:      []string{"10.1.2.3:8443"},
		Scheme:     "https",
		ServerName: "api.example.com",
		OTel:       &off,
	})
	require.NoError(t, err)

	reqRT := cli.GetTransport()
	require.NotNil(t, reqRT)
	require.NotNil(t, reqRT.TLSClientConfig)
	assert.Equal(t, "api.example.com", reqRT.TLSClientConfig.ServerName)

	rt := cli.GetClient().Transport.(*resolveTransport)
	assert.True(t, rt.preserveHost)
}

func TestClient_SeedOnlyHTTPSKeepsRewriteBehavior(t *testing.T) {
	f := NewFactory(nil, nil)
	defer f.Close()
	off := false

	// 仅种子地址 + https 保持原有的「端点即 SNI」行为，因此 IP 证书
	// （例如 httptest.NewTLSServer）依然可用。
	cli, err := f.Client(ClientInput{
		Seeds:  []string{"127.0.0.1:8443"},
		Scheme: "https",
		OTel:   &off,
	})
	require.NoError(t, err)

	rt := cli.GetClient().Transport.(*resolveTransport)
	assert.False(t, rt.preserveHost)
}

func TestNextWatchIndex_ResetsOnRollback(t *testing.T) {
	assert.Equal(t, uint64(10), nextWatchIndex(5, 10))
	assert.Equal(t, uint64(5), nextWatchIndex(5, 5))
	assert.Equal(t, uint64(0), nextWatchIndex(10, 3), "index rollback must reset to 0 to avoid a hot loop")
	assert.Equal(t, uint64(0), nextWatchIndex(10, 0))
}

func TestResolveTransport_AutoPreserveOnlyForIPEndpoints(t *testing.T) {
	cases := []struct {
		name        string
		ep          string
		wantURLHost string
		wantDialEP  string
	}{
		{"ip", "10.1.2.3:8443", "orders", "10.1.2.3:8443"},
		{"dns", "api.internal:8443", "api.internal:8443", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := &captureTransport{}
			rt := &resolveTransport{
				pool:             newEndpointPool([]string{tc.ep}),
				scheme:           "https",
				name:             "orders",
				maxFailover:      1,
				next:             capture,
				preserveHost:     true,
				autoPreserveHost: true,
				logicalHost:      "orders",
			}
			req, err := http.NewRequest(http.MethodGet, "https://orders/v1", nil)
			require.NoError(t, err)
			resp, err := rt.RoundTrip(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())

			assert.Equal(t, tc.wantURLHost, capture.req.URL.Host)
			assert.Equal(t, tc.wantDialEP, capture.dialEP)
		})
	}
}

func TestBuildTransport_NilBaseClonesDefault(t *testing.T) {
	rt, err := buildTransport(buildTransportInput{
		Pool:   newEndpointPool([]string{"10.1.2.3:8080"}),
		Scheme: "http",
		Name:   "orders",
	})
	require.NoError(t, err)

	resolve, ok := rt.(*resolveTransport)
	require.True(t, ok)
	base, ok := resolve.next.(*http.Transport)
	require.True(t, ok)
	assert.NotSame(t, http.DefaultTransport, base, "the global transport must never be shared")
	assert.GreaterOrEqual(t, base.MaxIdleConnsPerHost, defaultMaxIdleConnsPerHost)
	assert.GreaterOrEqual(t, base.MaxIdleConns, defaultMaxIdleConns)
	assert.GreaterOrEqual(t, base.IdleConnTimeout, defaultIdleConnTimeout)
}
