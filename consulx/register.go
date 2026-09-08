package consulx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/fitan/fxkit/server"
	"go.uber.org/fx"
)

const (
	defaultTTL             = "15s"
	defaultDeregisterAfter = "1m"
	ttlPassInterval        = 5 * time.Second
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
	fx.In
	LC     fx.Lifecycle
	Disc   *Config
	App    *config.App          `optional:"true"`
	Server *server.Config       `optional:"true"`
	HTTP   *server.HTTPServer   `optional:"true"` // start-order: listen before register
}

// registerSelfLifecycle registers app.name on the Consul agent when discovery.register is true.
// Uses a TTL check (process heartbeats) so remote/Docker Consul need not HTTP-probe the app.
func registerSelfLifecycle(p registerSelfParams) {
	if p.Disc == nil {
		return
	}
	_ = p.HTTP // depend on listener so the port is bound before TTL registration
	var cancelTTL context.CancelFunc
	var registeredID string
	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			id, err := registerSelf(ctx, p.Disc, p.App, p.Server)
			if err != nil {
				if isRegisterConfigError(err) {
					return err
				}
				slog.Warn("consul register skipped", "error", err)
				return nil
			}
			if id == "" {
				return nil
			}
			registeredID = id
			ttlCtx, cancel := context.WithCancel(context.Background())
			cancelTTL = cancel
			go ttlPassLoop(ttlCtx, p.Disc, id)
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

func registerSelf(ctx context.Context, disc *Config, app *config.App, srv *server.Config) (string, error) {
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
	config.ApplyConsulToken(req)
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

func ttlPassLoop(ctx context.Context, disc *Config, serviceID string) {
	if disc == nil {
		return
	}
	baseAddr := strings.TrimSpace(disc.ConsulAddress)
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
	config.ApplyConsulToken(req)
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

func deregisterSelf(ctx context.Context, disc *Config, serviceID string) error {
	if disc == nil {
		return nil
	}
	baseAddr := strings.TrimSpace(disc.ConsulAddress)
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
	config.ApplyConsulToken(req)
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
