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

type consulHealthEntry struct {
	Node struct {
		Address string `json:"Address"`
	} `json:"Node"`
	Service struct {
		Address string `json:"Address"`
		Port    int    `json:"Port"`
	} `json:"Service"`
}

type watchConfig struct {
	consulBase  string
	service     string
	passingOnly bool
	wait        time.Duration
	httpClient  *http.Client
}

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
		index = nextIndex
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

func formatConsulWait(d time.Duration) string {
	if d < time.Second {
		d = time.Second
	}
	if d > maxWatchWait {
		d = maxWatchWait
	}
	return strconv.Itoa(int(d/time.Second)) + "s"
}

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
