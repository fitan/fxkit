package reqx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/fitan/fxkit/config"
)

// 需要真实 Consul：FXKIT_TEST_CONSUL_ADDR=http://127.0.0.1:8500

func boolPtr(b bool) *bool { return &b }

func consulBase(t *testing.T) string {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("FXKIT_TEST_CONSUL_ADDR"))
	if addr == "" {
		t.Skip("set FXKIT_TEST_CONSUL_ADDR to run reqx discovery integration tests")
	}
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/")
}

func TestDiscoveryIntegration(t *testing.T) {
	base := consulBase(t)

	// 被调用的上游服务。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("pong"))
	}))
	defer upstream.Close()

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	port := u.Port()
	const serviceName = "fxkit-itest-reqx"

	regBody, _ := json.Marshal(map[string]any{
		"ID":      serviceName,
		"Name":    serviceName,
		"Address": "127.0.0.1",
		"Port":    mustAtoi(t, port),
	})
	req, err := http.NewRequest(http.MethodPut, base+"/v1/agent/service/register", strings.NewReader(string(regBody)))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status=%d", resp.StatusCode)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodPut, base+"/v1/agent/service/deregister/"+serviceName, nil)
		if r, err := http.DefaultClient.Do(req); err == nil {
			r.Body.Close()
		}
	})

	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("discovery.consul_address", base); err != nil {
		t.Fatal(err)
	}

	f := NewFactory(nil, cfg)
	defer f.Close()

	in := ClientInput{Name: serviceName, OTel: boolPtr(false)}

	// 初始发现是同步的（ensurePool 会 fetch 一次）。
	cli, err := f.Client(in)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	endpoints := f.Endpoints(in)
	if len(endpoints) == 0 {
		t.Fatalf("no endpoints discovered")
	}
	found := false
	for _, ep := range endpoints {
		if strings.HasSuffix(ep, ":"+port) {
			found = true
		}
	}
	if !found {
		t.Fatalf("endpoints %v do not contain port %s", endpoints, port)
	}

	// 通过服务名发起请求，transport 会把 host 改写到发现的端点。
	r, err := cli.R().Get("http://" + serviceName + "/ping")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if r.StatusCode != http.StatusOK || strings.TrimSpace(r.String()) != "pong" {
		t.Fatalf("unexpected response: %d %q", r.StatusCode, r.String())
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("bad port %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}
