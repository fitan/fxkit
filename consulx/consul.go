// Package consulx 对接 Consul agent：可选自注册（TTL 心跳）与构建元数据补丁。
//
// [discovery.consul_address] 为空时模块不启用。
// [discovery.register]=true 时以 app.name 注册，写入本机 IP，并 TTL 保活；停机注销。
// 另有元数据补丁：对已存在的同名同端口服务补全 version/commit 等（兼容 sidecar 先注册）。
// 配置错误会阻断启动；Consul 不可达时仅告警。
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
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
)

type agentService struct {
	ID                string            `json:"ID"`
	Service           string            `json:"Service"`
	Address           string            `json:"Address"`
	Port              int               `json:"Port"`
	Meta              map[string]string `json:"Meta"`
	Tags              []string          `json:"Tags"`
	EnableTagOverride bool              `json:"EnableTagOverride"`
	Kind              string            `json:"Kind,omitempty"`
	Weights           map[string]int    `json:"Weights,omitempty"`
}

type agentCheck struct {
	CheckID                        string              `json:"CheckID,omitempty"`
	Name                           string              `json:"Name,omitempty"`
	Notes                          string              `json:"Notes,omitempty"`
	Status                         string              `json:"Status,omitempty"`
	ServiceID                      string              `json:"ServiceID,omitempty"`
	HTTP                           string              `json:"HTTP,omitempty"`
	TCP                            string              `json:"TCP,omitempty"`
	GRPC                           string              `json:"GRPC,omitempty"`
	GRPCUseTLS                     bool                `json:"GRPCUseTLS,omitempty"`
	Interval                       string              `json:"Interval,omitempty"`
	Timeout                        string              `json:"Timeout,omitempty"`
	TTL                            string              `json:"TTL,omitempty"`
	DeregisterCriticalServiceAfter string              `json:"DeregisterCriticalServiceAfter,omitempty"`
	TLSSkipVerify                  bool                `json:"TLSSkipVerify,omitempty"`
	Method                         string              `json:"Method,omitempty"`
	Header                         map[string][]string `json:"Header,omitempty"`
	SuccessBeforePassing           int                 `json:"SuccessBeforePassing,omitempty"`
	FailuresBeforeCritical         int                 `json:"FailuresBeforeCritical,omitempty"`
}

type registerServiceRequest struct {
	ID                string            `json:"ID"`
	Name              string            `json:"Name"`
	Address           string            `json:"Address,omitempty"`
	Port              int               `json:"Port"`
	Meta              map[string]string `json:"Meta,omitempty"`
	Tags              []string          `json:"Tags,omitempty"`
	EnableTagOverride bool              `json:"EnableTagOverride,omitempty"`
	Kind              string            `json:"Kind,omitempty"`
	Weights           map[string]int    `json:"Weights,omitempty"`
	Checks            []agentCheck      `json:"Checks,omitempty"`
}

// registerStartupMeta 装配 Fx OnStart hook，为配置端口上匹配 app.name 的注册项补全 Consul agent 元数据。
func registerStartupMeta(lc *di.Lifecycle, disc *Config, app *config.App, srv *server.Config) {
	if lc == nil {
		return
	}
	lc.Append(di.Hook{
		OnStart: func(ctx context.Context) error {
			buildinfo.Normalize()
			appName := ""
			if app != nil {
				appName = consulServiceName(app.Name)
			}
			targetPort := 0
			if srv != nil {
				targetPort, _ = strconv.Atoi(strings.TrimSpace(srv.Port))
			}
			if appName == "" || targetPort <= 0 {
				return nil
			}
			consulAddr := ""
			if disc != nil {
				consulAddr = strings.TrimSpace(disc.ConsulAddress)
			}
			if consulAddr == "" {
				return nil
			}

			httpClient := &http.Client{Timeout: 8 * time.Second}
			meta := buildinfo.Meta()

			var lastErr error
			for i := 0; i < 6; i++ {
				if err := patchConsulServiceMeta(ctx, httpClient, consulAddr, appName, targetPort, meta); err == nil {
					slog.Info("consul meta patched",
						"service", appName,
						"port", targetPort,
						"version", buildinfo.Version,
						"commit", buildinfo.Commit,
					)
					return nil
				} else {
					lastErr = err
				}
				select {
				case <-ctx.Done():
					// 软失败：Consul 在启动时是可选的；不要中止进程。
					slog.Warn("consul meta patch canceled",
						"service", appName, "port", targetPort, "err", ctx.Err(),
					)
					return nil
				case <-time.After(800 * time.Millisecond):
				}
			}
			slog.Warn("consul meta patch skipped",
				"service", appName, "port", targetPort, "err", lastErr,
			)
			return nil
		},
	})
}

// consulServiceName 是写入 Consul catalog 的逻辑服务名（app.name）。
func consulServiceName(appNameFromConfig string) string {
	return strings.TrimSpace(appNameFromConfig)
}

// consulServiceID 唯一标识一个副本：name + advertise 地址 + 端口。
// 当多台主机暴露同一监听端口时，仅用 Name+port 会冲突。
func consulServiceID(name, addr string, port int) string {
	return name + "-" + sanitizeConsulIDPart(addr) + "-" + strconv.Itoa(port)
}

func sanitizeConsulIDPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "unknown"
	}
	return out
}

func patchConsulServiceMeta(
	ctx context.Context,
	client *http.Client,
	consulAddr string,
	appName string,
	targetPort int,
	extraMeta map[string]string,
) error {
	base, err := normalizeConsulAddr(consulAddr)
	if err != nil {
		return err
	}
	services, err := listAgentServices(ctx, client, base)
	if err != nil {
		return err
	}
	checksByService, err := listAgentChecksByService(ctx, client, base)
	if err != nil {
		return err
	}

	var matched int
	for _, svc := range services {
		if !strings.EqualFold(strings.TrimSpace(svc.Service), appName) {
			continue
		}
		if svc.Port != targetPort {
			continue
		}
		if err := registerServiceWithMeta(ctx, client, base, svc, checksByService[svc.ID], extraMeta); err != nil {
			return err
		}
		matched++
	}
	if matched == 0 {
		return fmt.Errorf("no consul agent service matched app=%s port=%d", appName, targetPort)
	}
	return nil
}

func normalizeConsulAddr(addr string) (string, error) {
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

func listAgentServices(ctx context.Context, client *http.Client, base string) ([]agentService, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/agent/services", nil)
	if err != nil {
		return nil, err
	}
	config.ApplyConsulToken(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("consul list services status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	raw := map[string]agentService{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]agentService, 0, len(raw))
	for _, v := range raw {
		out = append(out, v)
	}
	return out, nil
}

func listAgentChecksByService(ctx context.Context, client *http.Client, base string) (map[string][]agentCheck, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/agent/checks", nil)
	if err != nil {
		return nil, err
	}
	config.ApplyConsulToken(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("consul list checks status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	raw := map[string]agentCheck{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := map[string][]agentCheck{}
	for _, c := range raw {
		sid := strings.TrimSpace(c.ServiceID)
		if sid == "" {
			continue
		}
		// 丢弃 status/runtime 字段；保留定义类字段以便重新注册。
		def := agentCheck{
			CheckID:                        c.CheckID,
			Name:                           c.Name,
			Notes:                          c.Notes,
			ServiceID:                      c.ServiceID,
			HTTP:                           c.HTTP,
			TCP:                            c.TCP,
			GRPC:                           c.GRPC,
			GRPCUseTLS:                     c.GRPCUseTLS,
			Interval:                       c.Interval,
			Timeout:                        c.Timeout,
			TTL:                            c.TTL,
			DeregisterCriticalServiceAfter: c.DeregisterCriticalServiceAfter,
			TLSSkipVerify:                  c.TLSSkipVerify,
			Method:                         c.Method,
			Header:                         c.Header,
			SuccessBeforePassing:           c.SuccessBeforePassing,
			FailuresBeforeCritical:         c.FailuresBeforeCritical,
		}
		out[sid] = append(out[sid], def)
	}
	return out, nil
}

func registerServiceWithMeta(
	ctx context.Context,
	client *http.Client,
	base string,
	svc agentService,
	checks []agentCheck,
	extraMeta map[string]string,
) error {
	meta := map[string]string{}
	for k, v := range svc.Meta {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		meta[k] = v
	}
	for k, v := range extraMeta {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		meta[k] = v
	}

	payload := registerServiceRequest{
		ID:                svc.ID,
		Name:              svc.Service,
		Address:           svc.Address,
		Port:              svc.Port,
		Meta:              meta,
		Tags:              svc.Tags,
		EnableTagOverride: svc.EnableTagOverride,
		Kind:              svc.Kind,
		Weights:           svc.Weights,
		Checks:            checks,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/v1/agent/service/register", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	config.ApplyConsulToken(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("consul register status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// Module 将 Consul 自注册、元数据补丁与分布式锁接入 DI。
// discovery.consul_address 为空时为无操作；discovery.register 控制是否自注册。
var Module = di.Module("fxkit/consulx",
	config.Provide[Config]("discovery"),
	func(i di.Injector) {
		lc, _ := di.Invoke[*di.Lifecycle](i)
		disc, _ := di.Invoke[*Config](i)
		app, _ := di.Invoke[*config.App](i)
		srv, _ := di.Invoke[*server.Config](i)

		// 自注册必须发生在 listener 已绑定端口之后，而 listener 是在
		// server.ListenerModule 的 PreStart（PhaseMountHTTP+1）里才被构造的。
		//
		// 不要在装配期 di.Invoke[*server.HTTPServer]：那会提前构建 *chi.Mux，
		// 使宿主模块之后贡献的 middleware / route 被静默丢弃。
		// 这里用更高的 PreStart 优先级：先解析 HTTPServer（把 bind hook 追加到
		// lc.hooks），再追加 consul 的 OnStart hook，从而保证「先监听再注册」。
		if lc != nil {
			lc.AppendPreStartPhase(di.PhaseMountHTTP+10, di.Hook{
				OnStart: func(context.Context) error {
					if disc != nil && disc.Register {
						if _, err := di.Invoke[*server.HTTPServer](i); err != nil {
							return fmt.Errorf("consulx: resolve http server for registration: %w", err)
						}
					}
					registerSelfLifecycle(registerSelfParams{
						LC:     lc,
						Disc:   disc,
						App:    app,
						Server: srv,
					})
					registerStartupMeta(lc, disc, app, srv)
					return nil
				},
			})
		} else {
			registerSelfLifecycle(registerSelfParams{Disc: disc, App: app, Server: srv})
			registerStartupMeta(lc, disc, app, srv)
		}

		// 提供分布式锁 Locker。
		// 地址非法时直接让启动失败，而不是静默降级为 disabledLocker。
		di.Provide(i, func(i di.Injector) (Locker, error) {
			discConfig, _ := di.Invoke[*Config](i)
			return BuildLocker(discConfig)
		})
	},
)
