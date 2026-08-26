package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/hatchetx"
	"go.uber.org/fx"
)

// EventPublisher publishes raw payload bytes to a topic (Hatchet event key = topic name).
type EventPublisher interface {
	PublishEvent(ctx context.Context, pubsubName, topicName string, data []byte, metadata map[string]string) error
}

// hatchetPublisher pushes outbox rows as Hatchet events (event key = topic name).
type hatchetPublisher struct {
	client *hatchetx.Client
}

func (p *hatchetPublisher) PublishEvent(ctx context.Context, _, topicName string, data []byte, metadata map[string]string) error {
	if p == nil || p.client == nil || !p.client.Enabled() {
		return errors.New("outbox: nil hatchet client")
	}
	return p.client.PushEventJSON(ctx, topicName, data, metadata)
}

// Relay polls the outbox table and publishes pending events.
type Relay struct {
	client    *gormx.Client
	publisher EventPublisher
	cfg       Config

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRelayParams is the Fx-friendly constructor input for [NewRelay].
type NewRelayParams struct {
	fx.In

	Client     *gormx.Client
	Hatchet    *hatchetx.Client `optional:"true"`
	Outbox     *Config
	HatchetCfg *hatchetx.Config `optional:"true"`
}

// NewRelay builds a Relay that publishes via Hatchet when hatchet.outbox_publisher is enabled.
func NewRelay(p NewRelayParams) *Relay {
	outboxCfg := Config{}
	if p.Outbox != nil {
		outboxCfg = *p.Outbox
	}
	var pub EventPublisher
	if p.HatchetCfg != nil && p.HatchetCfg.Enabled && p.HatchetCfg.OutboxPublisher && p.Hatchet != nil && p.Hatchet.Enabled() {
		pub = &hatchetPublisher{client: p.Hatchet}
		slog.Info("outbox: using Hatchet event publisher")
	}
	return &Relay{
		client:    p.Client,
		publisher: pub,
		cfg:       outboxCfg,
	}
}

// SetPublisher replaces the publisher (for tests).
func (r *Relay) SetPublisher(pub EventPublisher) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.publisher = pub
	r.mu.Unlock()
}

func (r *Relay) getPublisher() EventPublisher {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.publisher
}

// batchResult is the outcome of one [Relay.processBatch] call.
type batchResult struct {
	Published int
	Claimed   int
}

// ProcessBatch claims and publishes up to batch_size pending (or stale processing) events.
// The int result is the number of rows marked published.
func (r *Relay) ProcessBatch(ctx context.Context) (int, error) {
	res, err := r.processBatch(ctx)
	return res.Published, err
}

func (r *Relay) processBatch(ctx context.Context) (batchResult, error) {
	if r == nil || r.client == nil {
		return batchResult{}, fmt.Errorf("outbox: nil relay client")
	}
	pub := r.getPublisher()
	if pub == nil {
		return batchResult{}, fmt.Errorf("outbox: nil publisher")
	}
	batchSize := r.cfg.BatchSize
	maxRetries := r.cfg.MaxRetries
	claimTimeout := r.cfg.ClaimTimeout
	staleBefore := time.Now().UTC().Add(-claimTimeout)

	events, err := claimBatch(ctx, r.client.Conn(ctx), claimParams{
		Limit:       batchSize,
		StaleBefore: staleBefore,
	})
	if err != nil {
		return batchResult{}, err
	}
	if len(events) == 0 {
		return batchResult{}, nil
	}
	out := batchResult{Claimed: len(events)}

	hbCtx, hbCancel := context.WithCancel(ctx)
	var hbwg sync.WaitGroup
	hbwg.Add(1)
	go func() {
		defer hbwg.Done()
		r.heartbeat(hbCtx, heartbeatInput{
			LeaseID:      events[0].LeaseID,
			ClaimTimeout: claimTimeout,
		})
	}()
	defer func() {
		hbCancel()
		hbwg.Wait()
	}()

	for _, ev := range events {
		meta := publishMetadata(ev)
		if err := pub.PublishEvent(ctx, ev.Pubsub, ev.Topic, ev.Payload, meta); err != nil {
			n, markErr := r.markFailed(ctx, ev, maxRetries, err)
			if markErr != nil {
				slog.ErrorContext(ctx, "outbox: markFailed after publish error",
					"id", ev.ID, "error", markErr,
				)
				continue
			}
			if n == 0 {
				slog.InfoContext(ctx, "outbox: markFailed skipped (lease lost)",
					"id", ev.ID, "lease_id", ev.LeaseID,
				)
				continue
			}
			slog.WarnContext(ctx, "outbox: publish failed, will retry",
				"id", ev.ID, "topic", ev.Topic, "attempts", ev.Attempts, "error", err,
			)
			continue
		}
		n, err := r.markPublished(ctx, ev)
		if err != nil {
			slog.ErrorContext(ctx, "outbox: markPublished failed (may republish after claim timeout)",
				"id", ev.ID, "error", err,
			)
			continue
		}
		if n == 0 {
			slog.InfoContext(ctx, "outbox: markPublished skipped (lease lost)",
				"id", ev.ID, "lease_id", ev.LeaseID,
			)
			continue
		}
		out.Published++
		slog.InfoContext(ctx, "outbox: published",
			"id", ev.ID, "topic", ev.Topic, "pubsub", ev.Pubsub,
			"idempotency_key", idempotencyKeyValue(ev),
		)
	}
	return out, nil
}

func publishMetadata(ev OutboxEvent) map[string]string {
	key := idempotencyKeyValue(ev)
	if key == "" {
		return nil
	}
	return map[string]string{MetadataIdempotencyKey: key}
}

func idempotencyKeyValue(ev OutboxEvent) string {
	if ev.IdempotencyKey == nil {
		return ""
	}
	return *ev.IdempotencyKey
}

type heartbeatInput struct {
	LeaseID      string
	ClaimTimeout time.Duration
}

func (r *Relay) heartbeat(ctx context.Context, in heartbeatInput) {
	if in.LeaseID == "" {
		return
	}
	interval := in.ClaimTimeout / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			if err := r.client.Conn(ctx).Model(&OutboxEvent{}).
				Where("lease_id = ? AND status = ?", in.LeaseID, StatusProcessing).
				Update("locked_at", now).Error; err != nil && !errors.Is(err, context.Canceled) {
				slog.WarnContext(ctx, "outbox: lease heartbeat failed",
					"lease_id", in.LeaseID, "error", err,
				)
			}
		}
	}
}

func (r *Relay) markPublished(ctx context.Context, ev OutboxEvent) (int64, error) {
	now := time.Now().UTC()
	res := r.client.Conn(ctx).Model(&OutboxEvent{}).
		Where("id = ? AND lease_id = ? AND status = ?", ev.ID, ev.LeaseID, StatusProcessing).
		Updates(map[string]any{
			"status":       StatusPublished,
			"published_at": now,
			"last_error":   "",
			"locked_at":    nil,
			"lease_id":     "",
		})
	return res.RowsAffected, res.Error
}

func (r *Relay) markFailed(ctx context.Context, ev OutboxEvent, maxRetries int, pubErr error) (int64, error) {
	status := StatusPending
	if ev.Attempts >= maxRetries {
		status = StatusFailed
	}
	res := r.client.Conn(ctx).Model(&OutboxEvent{}).
		Where("id = ? AND lease_id = ? AND status = ?", ev.ID, ev.LeaseID, StatusProcessing).
		Updates(map[string]any{
			"status":     status,
			"last_error": pubErr.Error(),
			"locked_at":  nil,
			"lease_id":   "",
		})
	return res.RowsAffected, res.Error
}

// Run polls until Stop is called or ctx is cancelled.
func (r *Relay) Run(ctx context.Context) {
	if r == nil {
		return
	}
	interval := r.cfg.PollInterval
	if interval <= 0 {
		slog.Error("outbox: poll_interval must be > 0")
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancel = cancel
	r.wg.Add(1)
	r.mu.Unlock()
	defer r.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		res, err := r.processBatch(runCtx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.ErrorContext(runCtx, "outbox: relay batch failed", "error", err)
		}
		// Full batch ⇒ likely more pending; drain without waiting a full poll tick.
		// Empty/partial batches wait so a persistent error cannot hot-loop.
		if err == nil && res.Claimed > 0 && res.Claimed >= r.cfg.BatchSize {
			continue
		}
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Stop cancels the Run loop and waits for it to exit.
func (r *Relay) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}
