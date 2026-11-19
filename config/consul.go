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

	"github.com/fitan/fxkit/internal/consuladdr"
)

const (
	// defaultConsulKVTimeout 是读取 Consul KV 的默认超时。
	defaultConsulKVTimeout = 8 * time.Second
	// envConsulHTTPToken 是携带 Consul ACL token 的环境变量名。
	envConsulHTTPToken = "CONSUL_HTTP_TOKEN"
)

// fetchConsulKVInput 是 [fetchConsulKV] 的参数结构体。
type fetchConsulKVInput struct {
	// Address 是 Consul HTTP 地址。
	Address string
	// Key 是 Consul KV key，使用时去掉首尾斜杠。
	Key   string
	Token string // 可选；为空时回退到 CONSUL_HTTP_TOKEN
	// Timeout 是请求超时；<=0 时用 defaultConsulKVTimeout。
	Timeout time.Duration
}

// fetchConsulKV 从 Consul 读取原始 KV 值（GET /v1/kv/{key}?raw）。
func fetchConsulKV(ctx context.Context, in fetchConsulKVInput) ([]byte, error) {
	base, err := consuladdr.Normalize(in.Address)
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
		token = ConsulHTTPToken()
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

// ConsulHTTPToken 返回 CONSUL_HTTP_TOKEN（已 trim）；未设置则为空。
func ConsulHTTPToken() string {
	return strings.TrimSpace(os.Getenv(envConsulHTTPToken))
}

// ApplyConsulToken 在 CONSUL_HTTP_TOKEN 非空时设置 X-Consul-Token。
func ApplyConsulToken(req *http.Request) {
	if req == nil {
		return
	}
	if tok := ConsulHTTPToken(); tok != "" {
		req.Header.Set("X-Consul-Token", tok)
	}
}
