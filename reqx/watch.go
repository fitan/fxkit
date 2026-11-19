package reqx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fitan/fxkit/config"
)

// consulHealthEntry 是 Consul 健康服务查询返回条目的子集。
type consulHealthEntry struct {
	// Node 是注册该服务的 Consul 节点。
	Node struct {
		// Address 是节点地址；Service.Address 为空时使用。
		Address string `json:"Address"`
	} `json:"Node"`
	// Service 是服务实例。
	Service struct {
		// Address 是服务实例地址。
		Address string `json:"Address"`
		// Port 是服务实例端口。
		Port int `json:"Port"`
	} `json:"Service"`
}

// watchConfig 是单个服务的 Consul blocking query 监听配置。
type watchConfig struct {
	// consulBase 是 Consul 基础 URL（已归一化）。
	consulBase string
	// service 是 Consul 服务名。
	service string
	// passingOnly 为 true 时只取健康检查 passing 的实例。
	passingOnly bool
	// wait 是 blocking query 的最大挂起时长，<=0 时用默认 5m。
	wait time.Duration
	// httpClient 是查询 Consul 用的 HTTP client，nil 时按 wait 推导超时。
	httpClient *http.Client
}

// run 循环执行 Consul blocking query，把端点变化同步到 pool，直到 ctx 结束。
// wait/httpClient 未设置时使用默认值；查询失败时记录日志并间隔 2s 重试。
func (w watchConfig) run(ctx context.Context, pool *endpointPool) {
	if w.wait <= 0 {
		w.wait = defaultWatchWait
	}
	if w.httpClient == nil {
		w.httpClient = &http.Client{Timeout: w.wait + 10*time.Second}
	}

	var index uint64
	for {
		if ctx.Err() != nil {
			return
		}
		eps, nextIndex, err := w.fetch(ctx, index)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("reqx: consul watch failed",
				"service", w.service,
				"error", err,
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		index = nextWatchIndex(index, nextIndex)
		if len(eps) == 0 {
			if pool.len() > 0 {
				slog.Warn("reqx: consul returned no endpoints, clearing pool",
					"service", w.service,
				)
				pool.replace(nil)
			}
			continue
		}
		if endpointsEqual(pool.snapshot(), eps) {
			continue
		}
		pool.replace(eps)
		slog.Debug("reqx: endpoints updated",
			"service", w.service,
			"count", len(eps),
			"endpoints", eps,
		)
	}
}

// fetch 以 index 发起一次 Consul 健康查询，返回归一化端点与下一次 blocking query 的 index。
// passingOnly 控制是否只取 passing 实例。
func (w watchConfig) fetch(ctx context.Context, index uint64) ([]string, uint64, error) {
	u, err := url.Parse(w.consulBase + "/v1/health/service/" + url.PathEscape(w.service))
	if err != nil {
		return nil, index, err
	}
	q := u.Query()
	if w.passingOnly {
		q.Set("passing", "true")
	} else {
		q.Set("passing", "false")
	}
	if index > 0 {
		q.Set("index", strconv.FormatUint(index, 10))
	}
	q.Set("wait", formatConsulWait(w.wait))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, index, err
	}
	config.ApplyConsulToken(req)
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, index, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, index, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, index, fmt.Errorf("consul health status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	next := index
	if v := strings.TrimSpace(resp.Header.Get("X-Consul-Index")); v != "" {
		if parsed, err := strconv.ParseUint(v, 10, 64); err == nil {
			next = parsed
		}
	}

	var entries []consulHealthEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, next, err
	}
	eps := make([]string, 0, len(entries))
	for _, e := range entries {
		addr := strings.TrimSpace(e.Service.Address)
		if addr == "" {
			addr = strings.TrimSpace(e.Node.Address)
		}
		ep := joinHostPort(addr, e.Service.Port)
		if ep == "" {
			continue
		}
		eps = append(eps, ep)
	}
	return normalizeEndpoints(eps), next, nil
}

// nextWatchIndex 计算下一次 Consul blocking query 的 index。
//
// Consul 在集群恢复/leader 切换后可能返回比本地更小的 index。若直接使用，
// 后续查询会立即返回（index 已"旧"），形成空转热循环；此时重置为 0，
// 让下一次请求重新建立基线。
func nextWatchIndex(current, fetched uint64) uint64 {
	if fetched < current {
		return 0
	}
	return fetched
}

// formatConsulWait 把时长夹到 [1s, 10m] 并格式化为 Consul wait 参数（秒）。
func formatConsulWait(d time.Duration) string {
	if d < time.Second {
		d = time.Second
	}
	if d > maxWatchWait {
		d = maxWatchWait
	}
	return strconv.Itoa(int(d/time.Second)) + "s"
}

// normalizeConsulBase 去掉首尾空白与末尾斜杠，缺少 scheme 时补 http://；地址为空时返回错误。
func normalizeConsulBase(addr string) (string, error) {
	a := strings.TrimSpace(strings.TrimRight(addr, "/"))
	if a == "" {
		return "", fmt.Errorf("empty consul address")
	}
	if !strings.HasPrefix(a, "http://") && !strings.HasPrefix(a, "https://") {
		a = "http://" + a
	}
	u, err := url.Parse(a)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(u.String(), "/"), nil
}
