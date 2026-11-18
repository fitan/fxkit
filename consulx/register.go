package consulx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/internal/consuladdr"
	"github.com/fitan/fxkit/server"
)

const (
	defaultTTL             = "15s"
	defaultDeregisterAfter = "1m"
	ttlPassInterval        = 5 * time.Second
	// ttlWarnAfterFailures 连续失败达到该次数后从 Debug 升到 Warn（带次数，避免刷屏）。
	ttlWarnAfterFailures = 2
	// ttlReregisterAfterFailures 连续失败达到该次数后尝试重新注册
	// （默认 3*5s ≈ 15s，即默认 TTL 窗口）。
	ttlReregisterAfterFailures = 3
)

type registerConfigError struct{ msg string }

func (e *registerConfigError) Error() string { return e.msg }

func registerConfigErrorf(format string, args ...any) error {
	return &registerConfigError{msg: fmt.Sprintf(format, args...)}
}

func isRegisterConfigError(err error) bool {
	var e *registerConfigError
	return errors.As(err, &e)
}

type registerSelfParams struct {
	LC     *di.Lifecycle
	Disc   *Config
	App    *config.App
	Server *server.Config
}

// registerSelfLifecycle 在 discovery.register 为 true 时，把 app.name 注册到 Consul agent。
// 使用 TTL 检查（进程心跳），这样远端/Docker 中的 Consul 无需对应用做 HTTP 探测。
func registerSelfLifecycle(p registerSelfParams) {
	if p.Disc == nil {
		return
	}
	var cancelTTL context.CancelFunc
	var registeredID string
	if p.LC != nil {
		p.LC.Append(di.Hook{
			OnStart: func(ctx context.Context) error {
				id, err := registerSelf(ctx, p.Disc, p.App, p.Server)
				if err != nil {
					// discovery.register=true 时，注册失败意味着实例拿不到流量。
					// 直接失败启动，避免带病运行（重启由编排系统处理）。
					return fmt.Errorf("consul self-register: %w", err)
				}
				if id == "" {
					return nil
				}
				registeredID = id
				ttlCtx, cancel := context.WithCancel(context.Background())
				cancelTTL = cancel
				// 恢复后若服务/检查已被 Consul 移除，需要重新注册。
				reregister := func(rctx context.Context) (string, error) {
					return registerSelfWithClient(rctx, &http.Client{Timeout: 8 * time.Second}, p.Disc, p.App, p.Server)
				}
				go ttlPassLoop(ttlCtx, p.Disc, id, reregister)
				return nil
			},
			OnStop: func(ctx context.Context) error {
				if cancelTTL != nil {
					cancelTTL()
				}
				if registeredID == "" {
					return nil
				}
				if err := deregisterSelf(ctx, p.Disc, registeredID); err != nil {
					slog.Warn("consul deregister skipped", "error", err)
				}
				return nil
			},
		})
	}
}

func registerSelf(ctx context.Context, disc *Config, app *config.App, srv *server.Config) (string, error) {
	return registerSelfWithClient(ctx, &http.Client{Timeout: 8 * time.Second}, disc, app, srv)
}

func registerSelfWithClient(ctx context.Context, client *http.Client, disc *Config, app *config.App, srv *server.Config) (string, error) {
	if disc == nil || !disc.Register {
		return "", nil
	}
	baseAddr := strings.TrimSpace(disc.ConsulAddress)
	if baseAddr == "" {
		return "", registerConfigErrorf("discovery.register=true requires discovery.consul_address")
	}
	name := ""
	if app != nil {
		name = consulServiceName(app.Name)
	}
	if name == "" {
		return "", registerConfigErrorf("app.name empty")
	}
	port := 0
	if srv != nil {
		port, _ = strconv.Atoi(strings.TrimSpace(srv.Port))
	}
	if port <= 0 {
		return "", registerConfigErrorf("invalid server.port")
	}

	adv := resolveAdvertise(disc)
	if isLoopbackAddr(adv.Addr) && !adv.Explicit {
		return "", registerConfigErrorf("discovery.register: advertise %q is loopback; set discovery.advertise_address", adv.Addr)
	}
	id := consulServiceID(name, adv.Addr, port)
	buildinfo.Normalize()
	meta := buildinfo.Meta()
	meta["module"] = "fxkit/consulx"

	payload := registerServiceRequest{
		ID:      id,
		Name:    name,
		Address: adv.Addr,
		Port:    port,
		Tags:    mergeRegisterTags(disc.Tags),
		Meta:    meta,
		Checks: []agentCheck{{
			CheckID:                        "service:" + id,
			Name:                           "TTL " + name,
			TTL:                            defaultTTL,
			DeregisterCriticalServiceAfter: defaultDeregisterAfter,
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	agentBase, err := consuladdr.Normalize(baseAddr)
	if err != nil {
		return "", err
	}
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, agentBase+"/v1/agent/service/register", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	config.ApplyConsulToken(req)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if _, err := readBodyAndCheck("consul register", resp); err != nil {
		return "", err
	}
	_ = passTTL(ctx, client, agentBase, id)
	slog.Info("consul service registered",
		"id", id, "name", name, "address", adv.Addr, "port", port, "check", "ttl:"+defaultTTL,
	)
	return id, nil
}

func mergeRegisterTags(extra []string) []string {
	out := []string{"fxkit", "http"}
	seen := map[string]struct{}{"fxkit": {}, "http": {}}
	for _, t := range extra {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// ttlPassLoop 以 production 默认参数运行 TTL 心跳。
func ttlPassLoop(ctx context.Context, disc *Config, serviceID string, reregister func(context.Context) (string, error)) {
	ttlPassLoopWithClient(ctx, &http.Client{Timeout: 3 * time.Second}, ttlPassInterval, disc, serviceID, reregister)
}

// ttlPassLoopWithClient 发送 TTL 心跳：
//   - 连续失败达到 ttlWarnAfterFailures 后从 Debug 升级为 Warn（带 failures 计数，避免刷屏）；
//   - 404（check/service 已不存在）或连续失败达到 ttlReregisterAfterFailures 时调用
//     reregister 重新注册，成功后退避计数归零。
func ttlPassLoopWithClient(
	ctx context.Context,
	client *http.Client,
	interval time.Duration,
	disc *Config,
	serviceID string,
	reregister func(context.Context) (string, error),
) {
	if disc == nil {
		return
	}
	baseAddr := strings.TrimSpace(disc.ConsulAddress)
	if baseAddr == "" || serviceID == "" {
		return
	}
	agentBase, err := consuladdr.Normalize(baseAddr)
	if err != nil {
		return
	}
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	if interval <= 0 {
		interval = ttlPassInterval
	}
	// 重新注册的冷却时间：避免 404 持续存在时每个 tick 都重注册。
	cooldown := 3 * interval

	t := time.NewTicker(interval)
	defer t.Stop()

	failures := 0
	var lastRegister time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := passTTL(ctx, client, agentBase, serviceID)
			if err == nil {
				if failures >= ttlWarnAfterFailures {
					slog.Info("consul ttl pass recovered", "id", serviceID, "failures", failures)
				}
				failures = 0
				continue
			}
			failures++
			if failures >= ttlWarnAfterFailures {
				slog.Warn("consul ttl pass failed", "id", serviceID, "failures", failures, "error", err)
			} else {
				slog.Debug("consul ttl pass failed", "id", serviceID, "failures", failures, "error", err)
			}

			removed := isStatusError(err, http.StatusNotFound)
			if !removed && failures < ttlReregisterAfterFailures {
				continue
			}
			if reregister == nil {
				continue
			}
			if !lastRegister.IsZero() && time.Since(lastRegister) < cooldown {
				continue
			}
			lastRegister = time.Now()

			newID, rerr := reregister(ctx)
			if rerr != nil {
				slog.Warn("consul re-register after ttl failures failed",
					"id", serviceID, "failures", failures, "error", rerr)
				continue
			}
			if newID != "" && newID != serviceID {
				slog.Warn("consul re-register changed service id", "old", serviceID, "new", newID)
			}
			slog.Info("consul service re-registered after ttl failures",
				"id", serviceID, "failures", failures)
			failures = 0
		}
	}
}

func passTTL(ctx context.Context, client *http.Client, agentBase, serviceID string) error {
	checkID := "service:" + serviceID
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, agentBase+"/v1/agent/check/pass/"+url.PathEscape(checkID), nil)
	if err != nil {
		return err
	}
	config.ApplyConsulToken(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = readBodyAndCheck("consul ttl pass", resp)
	return err
}

func deregisterSelf(ctx context.Context, disc *Config, serviceID string) error {
	if disc == nil {
		return nil
	}
	baseAddr := strings.TrimSpace(disc.ConsulAddress)
	if baseAddr == "" || serviceID == "" {
		return nil
	}
	agentBase, err := consuladdr.Normalize(baseAddr)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, agentBase+"/v1/agent/service/deregister/"+url.PathEscape(serviceID), nil)
	if err != nil {
		return err
	}
	config.ApplyConsulToken(req)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if _, err := readBodyAndCheck("consul deregister", resp); err != nil {
		return err
	}
	slog.Info("consul service deregistered", "id", serviceID)
	return nil
}
