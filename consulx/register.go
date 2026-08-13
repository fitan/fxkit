package consulx

import (
	"bytes"
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

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/config"
	"go.uber.org/fx"
)

const (
	defaultTTL              = "15s"
	defaultDeregisterAfter  = "1m"
	ttlPassInterval         = 5 * time.Second
)

// registerSelfLifecycle registers app.name on the Consul agent when discovery.register is true.
// Uses a TTL check (process heartbeats) so remote/Docker Consul need not HTTP-probe the app.
func registerSelfLifecycle(lc fx.Lifecycle, cfg *config.Config) {
	if cfg == nil {
		return
	}
	var cancelTTL context.CancelFunc
	var registeredID string
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			id, err := registerSelf(ctx, cfg)
			if err != nil {
				return err
			}
			if id == "" {
				return nil
			}
			registeredID = id
			ttlCtx, cancel := context.WithCancel(context.Background())
			cancelTTL = cancel
			go ttlPassLoop(ttlCtx, cfg, id)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancelTTL != nil {
				cancelTTL()
			}
			if registeredID == "" {
				return nil
			}
			if err := deregisterSelf(ctx, cfg, registeredID); err != nil {
				slog.Warn("consul deregister skipped", "error", err)
			}
			return nil
		},
	})
}

func registerSelf(ctx context.Context, cfg *config.Config) (string, error) {
	c := cfg.Get()
	if !c.Discovery.Register {
		return "", nil
	}
	baseAddr := strings.TrimSpace(c.Discovery.ConsulAddress)
	if baseAddr == "" {
		return "", fmt.Errorf("discovery.register=true requires discovery.consul_address")
	}
	name := consulServiceName(c.App.Name)
	if name == "" {
		return "", fmt.Errorf("app.name empty")
	}
	port, _ := strconv.Atoi(strings.TrimSpace(c.Server.Port))
	if port <= 0 {
		return "", fmt.Errorf("invalid server.port")
	}

	adv := resolveAdvertise(cfg)
	if isLoopbackAddr(adv.Addr) && !adv.Explicit {
		return "", fmt.Errorf("discovery.register: advertise %q is loopback; set discovery.advertise_address or FXKIT_ADVERTISE_ADDRESS", adv.Addr)
	}
	id := name + "-" + strconv.Itoa(port)
	buildinfo.Normalize()

	payload := registerServiceRequest{
		ID:      id,
		Name:    name,
		Address: adv.Addr,
		Port:    port,
		Tags:    []string{"fxkit", "http"},
		Meta: map[string]string{
			"version":        buildinfo.Version,
			"build_commit":   buildinfo.Commit,
			"build_time":     buildinfo.BuildTime,
			"build_built_by": buildinfo.BuiltBy,
			"module":         "fxkit/consulx",
		},
		Checks: []agentCheck{{
			TTL:                            defaultTTL,
			DeregisterCriticalServiceAfter: defaultDeregisterAfter,
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	agentBase, err := normalizeConsulAddr(baseAddr)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, agentBase+"/v1/agent/service/register", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("consul register status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	_ = passTTL(ctx, client, agentBase, id)
	slog.Info("consul service registered",
		"id", id, "name", name, "address", adv.Addr, "port", port, "check", "ttl:"+defaultTTL,
	)
	return id, nil
}

func ttlPassLoop(ctx context.Context, cfg *config.Config, serviceID string) {
	baseAddr := strings.TrimSpace(cfg.Get().Discovery.ConsulAddress)
	if baseAddr == "" || serviceID == "" {
		return
	}
	agentBase, err := normalizeConsulAddr(baseAddr)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	t := time.NewTicker(ttlPassInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := passTTL(ctx, client, agentBase, serviceID); err != nil {
				slog.Debug("consul ttl pass failed", "id", serviceID, "error", err)
			}
		}
	}
}

func passTTL(ctx context.Context, client *http.Client, agentBase, serviceID string) error {
	checkID := "service:" + serviceID
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, agentBase+"/v1/agent/check/pass/"+url.PathEscape(checkID), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func deregisterSelf(ctx context.Context, cfg *config.Config, serviceID string) error {
	baseAddr := strings.TrimSpace(cfg.Get().Discovery.ConsulAddress)
	if baseAddr == "" || serviceID == "" {
		return nil
	}
	agentBase, err := normalizeConsulAddr(baseAddr)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, agentBase+"/v1/agent/service/deregister/"+url.PathEscape(serviceID), nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("consul deregister status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	slog.Info("consul service deregistered", "id", serviceID)
	return nil
}
