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

func TestResolveTransport_POSTFailoverOn503(t *testing.T) {
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
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if hits.Load() < 2 {
		t.Fatalf("hits=%d want failover", hits.Load())
	}
}

func TestResolveTransport_POSTFailoverOnConnectionRefused(t *testing.T) {
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
