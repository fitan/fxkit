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
	"strconv"
	"strings"
	"time"

	"github.com/fitan/fxkit/buildinfo"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/internal/consuladdr"
	"github.com/fitan/fxkit/server"
)

// agentService 是 GET /v1/agent/services 返回的单个服务条目（Consul agent API 的子集）。
type agentService struct {
	// ID 服务 ID，在 agent 内唯一。
	ID string `json:"ID"`
	// Service 服务名，即 catalog 中的逻辑服务名。
	Service string `json:"Service"`
	// Address 服务地址（注册时写入的地址）。
	Address string `json:"Address"`
	// Port 服务端口。
	Port int `json:"Port"`
	// Meta 服务元数据键值对。
	Meta map[string]string `json:"Meta"`
	// Tags 服务标签。
	Tags []string `json:"Tags"`
	// EnableTagOverride 是否允许通过 catalog 接口修改标签。
	EnableTagOverride bool `json:"EnableTagOverride"`
	// Kind 服务类型（如 connect-proxy、terminating-gateway）；空表示普通服务。
	Kind string `json:"Kind,omitempty"`
	// Weights 服务权重（Passing/Warning）。
	Weights map[string]int `json:"Weights,omitempty"`
}

// agentCheck 是 GET /v1/agent/checks 返回的检查定义（Consul agent API 的子集）。
type agentCheck struct {
	// CheckID 检查 ID；服务级 TTL 检查通常为 service:<服务ID>。
	CheckID string `json:"CheckID,omitempty"`
	// Name 检查名称。
	Name string `json:"Name,omitempty"`
	// Notes 检查备注。
	Notes string `json:"Notes,omitempty"`
	// Status 检查状态（passing/warning/critical）；重新注册时会被丢弃。
	Status string `json:"Status,omitempty"`
	// ServiceID 所属服务 ID；为空表示节点级检查。
	ServiceID string `json:"ServiceID,omitempty"`
	// HTTP 健康检查请求的 URL。
	HTTP string `json:"HTTP,omitempty"`
	// TCP 健康检查的目标地址（host:port）。
	TCP string `json:"TCP,omitempty"`
	// GRPC 健康检查的目标地址（host:port）。
	GRPC string `json:"GRPC,omitempty"`
	// GRPCUseTLS gRPC 检查是否使用 TLS。
	GRPCUseTLS bool `json:"GRPCUseTLS,omitempty"`
	// Interval 检查间隔（如 "10s"）。
	Interval string `json:"Interval,omitempty"`
	// Timeout 检查超时（如 "1s"）。
	Timeout string `json:"Timeout,omitempty"`
	// TTL 检查存活时长（如 "15s"）；需通过 /v1/agent/check/pass 定期续期。
	TTL string `json:"TTL,omitempty"`
	// DeregisterCriticalServiceAfter 检查持续 critical 超过该时长后自动注销服务（如 "1m"）。
	DeregisterCriticalServiceAfter string `json:"DeregisterCriticalServiceAfter,omitempty"`
	// TLSSkipVerify 检查请求是否跳过 TLS 证书校验。
	TLSSkipVerify bool `json:"TLSSkipVerify,omitempty"`
	// Method HTTP 检查使用的请求方法，空表示 GET。
	Method string `json:"Method,omitempty"`
	// Header HTTP 检查附带的请求头。
	Header map[string][]string `json:"Header,omitempty"`
	// SuccessBeforePassing 从 warning/critical 转为 passing 前需要的连续成功次数。
	SuccessBeforePassing int `json:"SuccessBeforePassing,omitempty"`
	// FailuresBeforeCritical 转为 critical 前允许的连续失败次数。
	FailuresBeforeCritical int `json:"FailuresBeforeCritical,omitempty"`
}

// registerServiceRequest 是 PUT /v1/agent/service/register 的请求体。
type registerServiceRequest struct {
	// ID 服务 ID；同机同端口的多个副本必须各自唯一。
	ID string `json:"ID"`
	// Name 服务名，即 catalog 中的逻辑服务名。
	Name string `json:"Name"`
	// Address 服务地址；为空时 Consul 使用 agent 自身的地址。
	Address string `json:"Address,omitempty"`
	// Port 服务端口。
	Port int `json:"Port"`
	// Meta 服务元数据键值对。
	Meta map[string]string `json:"Meta,omitempty"`
	// Tags 服务标签。
	Tags []string `json:"Tags,omitempty"`
	// EnableTagOverride 是否允许通过 catalog 接口修改标签。
	EnableTagOverride bool `json:"EnableTagOverride,omitempty"`
	// Kind 服务类型；空表示普通服务。
	Kind string `json:"Kind,omitempty"`
	// Weights 服务权重（Passing/Warning）。
	Weights map[string]int `json:"Weights,omitempty"`
	// Checks 注册时一并创建的健康检查。
	Checks []agentCheck `json:"Checks,omitempty"`
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

			// 只有自注册（discovery.register）才需要「等注册完成再补元数据」。
			// 未开启自注册时（例如 sidecar 注册）只做一次尽力尝试，
			// 否则每次启动都会空等 6 轮（每轮 800ms，共 ~4.8s）。
			attempts := 1
			if disc.Register {
				attempts = 3
			}

			var lastErr error
			for i := 0; i < attempts; i++ {
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
				"service", appName, "port", targetPort, "err", lastErr)
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

// sanitizeConsulIDPart 把字符串转换为可写入 Consul 服务 ID 的片段：
// 仅保留字母、数字与 . - _，其余字符替换为 '-'；结果为空时返回 "unknown"。
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

// patchConsulServiceMeta 列出 agent 上的服务与检查，对同名同端口的注册项
// 合并 extraMeta 后重新注册；没有任何匹配项时返回错误。
func patchConsulServiceMeta(
	ctx context.Context,
	client *http.Client,
	consulAddr string,
	appName string,
	targetPort int,
	extraMeta map[string]string,
) error {
	base, err := consuladdr.Normalize(consulAddr)
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

// listAgentServices 调用 GET /v1/agent/services，把返回的 map 展开为切片；
// 非 2xx 时返回带状态码与响应体的错误。
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

// listAgentChecksByService 调用 GET /v1/agent/checks，按 ServiceID 分组返回检查定义。
// 只保留定义类字段（丢弃 status/runtime），便于重新注册；ServiceID 为空的检查被跳过。
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

// registerServiceWithMeta 以「原 meta + extraMeta（同名覆盖）」重新注册服务，
// 跳过键或值为空白的项，随后补发一次 TTL pass；pass 失败仅告警。
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
	// 重新注册会重建 TTL check，而新建的 TTL check 在首次 pass 之前是 critical。
	// 下一次心跳可能 5s 后才到，期间开启 passing_only 的发现会把实例摘掉。
	if err := passTTL(ctx, client, base, svc.ID); err != nil {
		slog.Warn("consul: pass TTL after metadata re-register failed",
			"id", svc.ID, "error", err)
	}
	return nil
}

// Module 将 Consul 自注册、元数据补丁与分布式锁接入 DI。
// discovery.consul_address 为空时为无操作；discovery.register 控制是否自注册。
var Module = di.Module("fxkit/consulx",
	config.Provide[Config]("discovery"),
	func(i di.Injector) {
		lc := di.MustInvoke[*di.Lifecycle](i)
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
			lc.AppendPreStartPhase(di.PhaseAfterHTTPServer, di.Hook{
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
