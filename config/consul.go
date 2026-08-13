package config

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultConsulKVTimeout = 8 * time.Second
	envConsulHTTPToken     = "CONSUL_HTTP_TOKEN"
)

// fetchConsulKVInput is the parameter struct for [fetchConsulKV].
type fetchConsulKVInput struct {
	Address string
	Key     string
	Token   string // optional; falls back to CONSUL_HTTP_TOKEN
	Timeout time.Duration
}

// fetchConsulKV reads a raw KV value from Consul (GET /v1/kv/{key}?raw).
func fetchConsulKV(ctx context.Context, in fetchConsulKVInput) ([]byte, error) {
	base, err := normalizeConsulHTTPAddr(in.Address)
	if err != nil {
		return nil, err
	}
	key := strings.Trim(strings.TrimSpace(in.Key), "/")
	if key == "" {
		return nil, fmt.Errorf("consul config key is required")
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = defaultConsulKVTimeout
	}
	token := strings.TrimSpace(in.Token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv(envConsulHTTPToken))
	}

	u, err := url.Parse(base + "/v1/kv/" + key)
	if err != nil {
		return nil, fmt.Errorf("consul kv url: %w", err)
	}
	q := u.Query()
	q.Set("raw", "true")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("X-Consul-Token", token)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("consul kv get %q: %w", key, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("consul kv read %q: %w", key, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("consul kv key %q not found", key)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("consul kv get %q status=%d body=%s", key, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, fmt.Errorf("consul kv key %q is empty", key)
	}
	return body, nil
}

func normalizeConsulHTTPAddr(addr string) (string, error) {
	a := strings.TrimSpace(strings.TrimRight(addr, "/"))
	if a == "" {
		return "", fmt.Errorf("empty consul address")
	}
	if !strings.HasPrefix(a, "http://") && !strings.HasPrefix(a, "https://") {
		a = "http://" + a
	}
	u, err := url.Parse(a)
	if err != nil {
		return "", fmt.Errorf("invalid consul address %q: %w", addr, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid consul address %q: missing host", addr)
	}
	return strings.TrimRight(u.String(), "/"), nil
}
