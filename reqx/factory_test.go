package reqx

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fitan/fxkit/config"
	"go.uber.org/fx/fxtest"
)

func TestParseEndpoint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"10.0.0.1:8080", "10.0.0.1:8080", true},
		{"api.example.com", "api.example.com", true},
		{"https://api.example.com:443/v1", "api.example.com:443", true},
		{"http://10.0.0.2:9090", "10.0.0.2:9090", true},
		{"", "", false},
		{"http://", "", false},
	}
	for _, tc := range cases {
		got, ok := parseEndpoint(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("parseEndpoint(%q)=(%q,%v) want (%q,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestEndpointPoolRoundRobin(t *testing.T) {
	t.Parallel()
	p := newEndpointPool([]string{"a:1", "b:2", "a:1"})
	if p.len() != 2 {
		t.Fatalf("len=%d want 2", p.len())
	}
	var got []string
	for i := 0; i < 4; i++ {
		ep, err := p.next()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ep)
	}
	want := []string{"a:1", "b:2", "a:1", "b:2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestClientStaticSeeds(t *testing.T) {
	t.Parallel()
	var seenHost, seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		seenPath = r.URL.Path
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)

	lc := fxtest.NewLifecycle(t)
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := NewFactory(lc, cfg)
	lc.RequireStart()
	t.Cleanup(func() { lc.RequireStop() })

	otelOff := false
	cli, err := f.Client(ClientInput{
		Seeds:   []string{srv.URL},
		OTel:    &otelOff,
		Scheme:  "http",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := cli.R().Get("/ping")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.String())
	}
	if seenPath != "/ping" {
		t.Fatalf("path=%q", seenPath)
	}
	if seenHost == "" {
		t.Fatal("expected request host")
	}
	eps := f.Endpoints(ClientInput{Seeds: []string{srv.URL}, OTel: &otelOff})
	if len(eps) != 1 {
		t.Fatalf("endpoints=%v", eps)
	}
}

func TestWatchFetchParsesEndpoints(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Consul-Index", "42")
		_, _ = io.WriteString(w, `[
			{"Service":{"Address":"10.1.2.3","Port":8080}},
			{"Node":{"Address":"api.internal"},"Service":{"Address":"","Port":8443}}
		]`)
	}))
	t.Cleanup(srv.Close)

	w := watchConfig{
		consulBase:  srv.URL,
		service:     "users",
		passingOnly: true,
		httpClient:  srv.Client(),
	}
	eps, idx, err := w.fetch(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if idx != 42 {
		t.Fatalf("index=%d", idx)
	}
	if len(eps) != 2 {
		t.Fatalf("eps=%v", eps)
	}
	want := map[string]bool{"10.1.2.3:8080": true, "api.internal:8443": true}
	for _, ep := range eps {
		if !want[ep] {
			t.Fatalf("unexpected endpoint %q in %v", ep, eps)
		}
	}
}

func TestClientRequiresNameOrSeeds(t *testing.T) {
	t.Parallel()
	lc := fxtest.NewLifecycle(t)
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := NewFactory(lc, cfg)
	lc.RequireStart()
	t.Cleanup(func() { lc.RequireStop() })

	_, err = f.Client(ClientInput{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFailoverToNextEndpoint(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(bad.Close)

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `ok`)
	}))
	t.Cleanup(good.Close)

	lc := fxtest.NewLifecycle(t)
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := NewFactory(lc, cfg)
	lc.RequireStart()
	t.Cleanup(func() { lc.RequireStop() })

	otelOff := false
	cli, err := f.Client(ClientInput{
		Seeds:   []string{bad.URL, good.URL},
		OTel:    &otelOff,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := cli.R().Get("/x")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.String())
	}
	if hits.Load() < 2 {
		t.Fatalf("expected failover hits>=2 got %d", hits.Load())
	}
}

func TestHTTPSScheme(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `ok`)
	}))
	t.Cleanup(srv.Close)

	lc := fxtest.NewLifecycle(t)
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := NewFactory(lc, cfg)
	lc.RequireStart()
	t.Cleanup(func() { lc.RequireStop() })

	otelOff := false
	cli, err := f.Client(ClientInput{
		Seeds:         []string{srv.URL},
		Scheme:        "https",
		OTel:          &otelOff,
		Timeout:       5 * time.Second,
		BaseTransport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := cli.R().Get("/secure")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestResolvePassingOnly(t *testing.T) {
	t.Parallel()
	f := false
	if resolvePassingOnly(&f, nil) {
		t.Fatal("override false")
	}
	if !resolvePassingOnly(nil, nil) {
		t.Fatal("nil cfg defaults true")
	}
}

func TestWatch_EmptyConsulKeepsCurrentAndContinues(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) > 1 {
			<-r.Context().Done()
			return
		}
		w.Header().Set("X-Consul-Index", "1")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	pool := newEndpointPool([]string{"10.1.2.3:8080"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go watchConfig{
		consulBase: srv.URL,
		service:    "orders",
		wait:       time.Millisecond,
		httpClient: srv.Client(),
	}.run(ctx, pool)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// Second Consul fetch starts only after the empty result was applied.
		if hits.Load() >= 2 {
			eps := pool.snapshot()
			if len(eps) != 1 || eps[0] != "10.1.2.3:8080" {
				t.Fatalf("empty consul must not replace pool, got %v", eps)
			}
			cancel()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("watch never fetched, hits=%d pool=%v", hits.Load(), pool.snapshot())
}

func TestResolveTransport_POSTFailoverRewindsBody(t *testing.T) {
	t.Parallel()
	var first, second string
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		first = string(b)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(fail.Close)
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		second = string(b)
		_, _ = io.WriteString(w, `ok`)
	}))
	t.Cleanup(ok.Close)

	failEP, okFail := parseEndpoint(fail.URL)
	okEP, okOK := parseEndpoint(ok.URL)
	if !okFail || !okOK {
		t.Fatalf("parse %q %q", fail.URL, ok.URL)
	}
	rt := &resolveTransport{
		pool:        newEndpointPool([]string{failEP, okEP}),
		scheme:      "http",
		name:        "test",
		maxFailover: 2,
		next:        http.DefaultTransport,
	}
	body := []byte(`{"hello":"world"}`)
	req, err := http.NewRequest(http.MethodPost, "http://test/v1", io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(len(body))
	req.GetBody = nil
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if first != string(body) {
		t.Fatalf("first body=%q", first)
	}
	if second != string(body) {
		t.Fatalf("second body=%q first=%q", second, first)
	}
}
