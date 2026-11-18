package reqx

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRewindBody_BuffersSmallBody(t *testing.T) {
	t.Parallel()
	body := []byte("hello")
	req, err := http.NewRequest(http.MethodPost, "http://x", io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	if err := rewindBody(req, 64); err != nil {
		t.Fatal(err)
	}
	if req.GetBody == nil {
		t.Fatal("expected GetBody")
	}
	got, err := io.ReadAll(req.Body)
	if err != nil || string(got) != "hello" {
		t.Fatalf("body=%q err=%v", got, err)
	}
	r2, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(r2)
	if err != nil || string(got) != "hello" {
		t.Fatalf("getBody=%q err=%v", got, err)
	}
}

func TestRewindBody_OverLimitPreservesRemainder(t *testing.T) {
	t.Parallel()
	body := []byte("abcdefghij")
	req, err := http.NewRequest(http.MethodPost, "http://x", io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1
	if err := rewindBody(req, 4); err != nil {
		t.Fatal(err)
	}
	if req.GetBody != nil {
		t.Fatal("over-limit must not set GetBody")
	}
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("got %q want %q", got, body)
	}
}

func TestRewindBody_ContentLengthOverLimitSkipsBuffer(t *testing.T) {
	t.Parallel()
	body := []byte("abcdefghij")
	req, err := http.NewRequest(http.MethodPost, "http://x", io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(len(body))
	if err := rewindBody(req, 4); err != nil {
		t.Fatal(err)
	}
	if req.GetBody != nil {
		t.Fatal("expected no GetBody")
	}
	got, err := io.ReadAll(req.Body)
	if err != nil || string(got) != string(body) {
		t.Fatalf("body=%q err=%v", got, err)
	}
}

func TestResolveTransport_POSTDoesNotFailoverWithoutIdempotencyKey(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(fail.Close)
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `ok`)
	}))
	t.Cleanup(ok.Close)

	rt := mustResolveTransport(t, fail.URL, ok.URL)
	req, err := http.NewRequest(http.MethodPost, "http://test/v1", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503 (POST must not be replayed)", resp.StatusCode)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d, non-idempotent request must not retry", hits.Load())
	}
}

type seqFailoverTransport struct{ calls int }

func (s *seqFailoverTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.calls++
	code := http.StatusOK
	if s.calls == 1 {
		code = http.StatusServiceUnavailable
	}
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     make(http.Header),
		Request:    r,
	}, nil
}

func TestResolveTransport_ClosesIdleOnFailoverWhenPreservingHost(t *testing.T) {
	t.Parallel()
	next := &seqFailoverTransport{}
	var closed atomic.Int32
	rt := &resolveTransport{
		pool:             newEndpointPool([]string{"127.0.0.1:1", "127.0.0.1:2"}),
		scheme:           "http",
		name:             "test",
		maxFailover:      2,
		next:             next,
		preserveHost:     true,
		autoPreserveHost: false,
		logicalHost:      "svc",
		closeIdleFn:      func() { closed.Add(1) },
	}

	req, err := http.NewRequest(http.MethodGet, "http://svc/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if closed.Load() == 0 {
		t.Fatal("preserveHost failover must close idle connections")
	}
}

func TestResolveTransport_POSTFailoverWithIdempotencyKey(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(fail.Close)
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `ok`)
	}))
	t.Cleanup(ok.Close)

	rt := mustResolveTransport(t, fail.URL, ok.URL)
	req, err := http.NewRequest(http.MethodPost, "http://test/v1", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Idempotency-Key", "evt-1")
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if hits.Load() < 2 {
		t.Fatalf("hits=%d, request with Idempotency-Key should failover", hits.Load())
	}
}

func TestResolveTransport_POSTDoesNotFailoverOnConnectionRefused(t *testing.T) {
	t.Parallel()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"hello":"world"}` {
			t.Errorf("body=%q", b)
		}
		_, _ = io.WriteString(w, `ok`)
	}))
	t.Cleanup(ok.Close)

	okEP, okOK := parseEndpoint(ok.URL)
	if !okOK {
		t.Fatalf("parse %q", ok.URL)
	}
	rt := &resolveTransport{
		pool:        newEndpointPool([]string{"127.0.0.1:1", okEP}),
		scheme:      "http",
		name:        "test",
		maxFailover: 2,
		next:        http.DefaultTransport,
	}
	body := []byte(`{"hello":"world"}`)
	req, err := http.NewRequest(http.MethodPost, "http://test/v1", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("non-idempotent request must not be replayed on another endpoint")
	}
}

func TestResolveTransport_POSTFailoverOnConnectionRefusedWithIdempotencyKey(t *testing.T) {
	t.Parallel()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"hello":"world"}` {
			t.Errorf("body=%q", b)
		}
		_, _ = io.WriteString(w, `ok`)
	}))
	t.Cleanup(ok.Close)

	okEP, okOK := parseEndpoint(ok.URL)
	if !okOK {
		t.Fatalf("parse %q", ok.URL)
	}
	rt := &resolveTransport{
		pool:        newEndpointPool([]string{"127.0.0.1:1", okEP}),
		scheme:      "http",
		name:        "test",
		maxFailover: 2,
		next:        http.DefaultTransport,
	}
	body := []byte(`{"hello":"world"}`)
	req, err := http.NewRequest(http.MethodPost, "http://test/v1", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Idempotency-Key", "evt-1")
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func mustResolveTransport(t *testing.T, failURL, okURL string) *resolveTransport {
	t.Helper()
	failEP, okFail := parseEndpoint(failURL)
	okEP, okOK := parseEndpoint(okURL)
	if !okFail || !okOK {
		t.Fatalf("parse %q %q", failURL, okURL)
	}
	return &resolveTransport{
		pool:        newEndpointPool([]string{failEP, okEP}),
		scheme:      "http",
		name:        "test",
		maxFailover: 2,
		next:        http.DefaultTransport,
	}
}

type fnRoundTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f fnRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f.fn(r)
}

// TestResolveTransport_ShrinkingPoolDoesNotSpin 回归：请求中途端点池变小不能空转。
func TestResolveTransport_ShrinkingPoolDoesNotSpin(t *testing.T) {
	t.Parallel()
	pool := newEndpointPool([]string{"10.0.0.1:1", "10.0.0.2:2", "10.0.0.3:3"})
	var calls atomic.Int32
	rt := &resolveTransport{
		pool:        pool,
		scheme:      "http",
		name:        "test",
		maxFailover: 5,
		next: fnRoundTripper{fn: func(r *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				pool.replace([]string{"10.0.0.1:1"})
			}
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("x")),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		}},
	}
	req, err := http.NewRequest(http.MethodGet, "http://svc/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	// 最后一次 502 会原样返回。次数必须等于快照长度，不能跟着变小后的池子空转。
	if got := calls.Load(); got != 3 {
		t.Fatalf("calls=%d, want 3 (the snapshot, not a spin)", got)
	}
}
