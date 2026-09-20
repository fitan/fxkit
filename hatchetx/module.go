// Package hatchetx wires the Hatchet Go SDK into Fx: client, optional worker,
// helpers for event push (outbox relay), workflow runs, and semantic actors
// ([NewActor] / [CreateReminder]).
//
// # Semantic actors (not Dapr Actors)
//
// [NewActor] provides a concurrency mailbox: Hatchet GROUP_ROUND_ROBIN with
// MaxRuns=1 keyed by CEL input.actorId. Same actorId runs serialize; different
// ids may run in parallel. This is not a full Dapr Actor / sticky entity worker:
//
//   - no process affinity across runs (sticky is for steps within one run)
//   - no in-memory activation; persist state outside the handler (DB, appkv, …)
//   - Call/CallAsync require the payload to JSON-serialize "actorId" (embed
//     [ActorRef] or a field with json:"actorId") so the concurrency CEL matches
//
// # Reminders
//
// [CreateReminder] is a recurring Hatchet cron, not a full Dapr Actor Reminder:
// period only (no dueTime / one-shot), missed ticks are not replayed, and
// create is not upsert (Name+Expression uniqueness may reject duplicates).
//
// # Events as MQ
//
// [Client.PushEvent] triggers tasks registered with matching event keys.
// Events that arrive before a task is registered are not replayed. Prefer the
// transactional outbox for producer durability; use inbox.Once for consumer
// idempotency (at-least-once delivery).
//
// Enable with hatchet.enabled=true and HATCHET_CLIENT_TOKEN (or hatchet.token).
// Worker traces (hatchet.start_step_run) attach when hatchet.otel is true (default)
// and otelx has installed an SDK TracerProvider; see attachWorkerTracing.
// See https://docs.hatchet.run/
package hatchetx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/fitan/fxkit/config"
	v0Client "github.com/hatchet-dev/hatchet/pkg/client"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/hatchet-dev/hatchet/sdks/go/features"
	"go.uber.org/fx"
)

// Client wraps the Hatchet SDK client. Nil when hatchet is disabled.
type Client struct {
	SDK *hatchet.Client
}

// WorkflowBase is re-exported for worker registration.
type WorkflowBase = hatchet.WorkflowBase

// Registrar contributes workflows/tasks to the shared worker.
type Registrar func(client *Client) ([]WorkflowBase, error)

// Module provides [Client] and, when enabled, starts a worker for all [Registrar]s.
var Module = fx.Module("fxkit/hatchetx",
	config.Provide[Config]("hatchet"),
	fx.Provide(NewClient),
	fx.Provide(fx.Annotate(defaultRegistrars, fx.ResultTags(`group:"hatchet_registrars,flatten"`))),
	provideOutboxPublisherOption(),
	fx.Invoke(registerLifecycle),
)

func defaultRegistrars() []Registrar { return nil }

// ProvideRegistrar publishes a [Registrar] into the hatchet_registrars fx group.
// Constructors must return [Registrar] (a function type — not an interface, so no fx.As).
func ProvideRegistrar(fn any) fx.Option {
	return fx.Provide(fx.Annotate(fn, fx.ResultTags(`group:"hatchet_registrars"`)))
}

// NewClient builds a Hatchet client when hatchet.enabled is true.
//
// The V1 Go SDK loads connection settings from HATCHET_CLIENT_* env vars
// (see https://docs.hatchet.run/home/migration-guide-go). Yaml/config values are
// applied as env defaults when the corresponding env var is unset — avoiding
// deprecated pkg/client WithToken / WithHostPort / WithNamespace opts.
func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil || !cfg.Enabled {
		return &Client{}, nil
	}
	if err := applyHatchetEnv(*cfg); err != nil {
		return nil, err
	}
	sdk, err := hatchet.NewClient()
	if err != nil {
		return nil, fmt.Errorf("hatchetx: %w", err)
	}
	return &Client{SDK: sdk}, nil
}

var envMu sync.Mutex

// applyHatchetEnv bridges fxkit yaml config into the env vars the SDK reads.
// Existing process env wins (do not overwrite).
func applyHatchetEnv(hcfg Config) error {
	envMu.Lock()
	defer envMu.Unlock()
	if hcfg.Token != "" {
		setenvIfEmpty("HATCHET_CLIENT_TOKEN", hcfg.Token)
	}
	if hp := strings.TrimSpace(hcfg.HostPort); hp != "" {
		if _, _, err := net.SplitHostPort(hp); err != nil {
			return fmt.Errorf("hatchetx: host_port: want host:port, got %q", hcfg.HostPort)
		}
		setenvIfEmpty("HATCHET_CLIENT_HOST_PORT", hp)
	}
	if u := strings.TrimSpace(hcfg.ServerURL); u != "" {
		setenvIfEmpty("HATCHET_CLIENT_SERVER_URL", u)
	}
	if hcfg.Namespace != "" {
		setenvIfEmpty("HATCHET_CLIENT_NAMESPACE", hcfg.Namespace)
	}
	// Local compose (SERVER_GRPC_INSECURE=t) needs plaintext gRPC.
	// yaml tls_strategy wins when env is unset; otherwise only loopback defaults to none.
	if s := strings.TrimSpace(hcfg.TLSStrategy); s != "" {
		setenvIfEmpty("HATCHET_CLIENT_TLS_STRATEGY", s)
	} else if isLoopbackHostPort(hcfg.HostPort) || isLoopbackHostPort(os.Getenv("HATCHET_CLIENT_HOST_PORT")) {
		setenvIfEmpty("HATCHET_CLIENT_TLS_STRATEGY", "none")
	}
	return nil
}

func isLoopbackHostPort(hp string) bool {
	hp = strings.TrimSpace(hp)
	if hp == "" {
		return false
	}
	host, _, err := net.SplitHostPort(hp)
	if err != nil {
		return false
	}
	host = strings.ToLower(host)
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func setenvIfEmpty(key, value string) {
	if os.Getenv(key) == "" {
		_ = os.Setenv(key, value)
	}
}

// Enabled reports whether the SDK client is available.
func (c *Client) Enabled() bool {
	return c != nil && c.SDK != nil
}

// PushEvent publishes a Hatchet event (triggers event-based tasks/workflows).
func (c *Client) PushEvent(ctx context.Context, eventKey string, payload any, metadata map[string]string) error {
	if !c.Enabled() {
		return fmt.Errorf("hatchetx: client disabled")
	}
	opts := []v0Client.PushOpFunc{}
	if len(metadata) > 0 {
		opts = append(opts, v0Client.WithEventMetadata(metadata))
	}
	return c.SDK.Events().Push(ctx, eventKey, payload, opts...)
}

// PushEventJSON unmarshals raw JSON into a map (or keeps raw string) and pushes.
func (c *Client) PushEventJSON(ctx context.Context, eventKey string, raw []byte, metadata map[string]string) error {
	var payload any
	if len(raw) == 0 {
		payload = map[string]any{}
	} else if err := json.Unmarshal(raw, &payload); err != nil {
		payload = map[string]any{"raw": string(raw)}
	}
	return c.PushEvent(ctx, eventKey, payload, metadata)
}

// RunNoWait starts a workflow and returns the run id.
func (c *Client) RunNoWait(ctx context.Context, workflowName string, input any) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("hatchetx: client disabled")
	}
	ref, err := c.SDK.RunNoWait(ctx, workflowName, input)
	if err != nil {
		return "", err
	}
	return ref.RunId, nil
}

// Crons returns the Hatchet cron trigger client, or nil when disabled.
func (c *Client) Crons() *features.CronsClient {
	if !c.Enabled() {
		return nil
	}
	return c.SDK.Crons()
}

type lifecycleParams struct {
	fx.In
	LC         fx.Lifecycle
	Cfg        *Config
	Client     *Client
	Registrars []Registrar `group:"hatchet_registrars"`
}

func registerLifecycle(p lifecycleParams) {
	if p.Cfg == nil || !p.Cfg.Enabled || !p.Client.Enabled() {
		return
	}
	workerName := p.Cfg.WorkerName

	var cleanup func() error
	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var workflows []WorkflowBase
			for _, reg := range p.Registrars {
				if reg == nil {
					continue
				}
				ws, err := reg(p.Client)
				if err != nil {
					return err
				}
				workflows = append(workflows, ws...)
			}
			if len(workflows) == 0 {
				slog.Info("hatchetx: enabled but no workflows registered; worker not started")
				return nil
			}
			w, err := p.Client.SDK.NewWorker(workerName, hatchet.WithWorkflows(workflows...))
			if err != nil {
				return fmt.Errorf("hatchetx: new worker: %w", err)
			}
			attachWorkerTracing(w, p.Cfg.OTel)
			cleanup, err = w.Start()
			if err != nil {
				return fmt.Errorf("hatchetx: start worker: %w", err)
			}
			slog.Info("hatchetx: worker started", "name", workerName, "workflows", len(workflows))
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cleanup != nil {
				if err := cleanup(); err != nil {
					slog.Warn("hatchetx: worker cleanup", "error", err)
					return err
				}
			}
			slog.Info("hatchetx: worker stopped")
			return nil
		},
	})
}
