package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fitan/fxkit/gormx"
	"go.uber.org/fx"
)

// EventPublisher publishes raw payload bytes to a topic (Hatchet event key = topic name).
// Inject implementations with [ProvidePublisher] (Hatchet is provided by hatchetx
// when hatchet.outbox_publisher is true). Last non-nil publisher wins.
type EventPublisher interface {
	PublishEvent(ctx context.Context, pubsubName, topicName string, data []byte, metadata map[string]string) error
}

// ProvidePublisher publishes an [EventPublisher] into the outbox_publishers fx group.
// Use this to drive the relay with Kafka / NATS / RabbitMQ instead of (or after) Hatchet.
func ProvidePublisher(fn any) fx.Option {
	return fx.Provide(fx.Annotate(fn, fx.As(new(EventPublisher)), fx.ResultTags(`group:"outbox_publishers"`)))
}

// Relay polls the outbox table and publishes pending events.
type Relay struct {
	client    *gormx.Client
	publisher EventPublisher
	cfg       Config

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
	notify chan struct{}
}

// NewRelayParams is the Fx-friendly constructor input for [NewRelay].
type NewRelayParams struct {
	fx.In

	Client     *gormx.Client
	Outbox     *Config
	Publishers []EventPublisher `group:"outbox_publishers"`
}

// NewRelay builds a Relay. The last non-nil [EventPublisher] in the fx group is used.
func NewRelay(p NewRelayParams) *Relay {
	outboxCfg := Config{}
	if p.Outbox != nil {
		outboxCfg = *p.Outbox
	}
	pub := pickPublisher(p.Publishers)
	if pub != nil {
		slog.Info("outbox: using EventPublisher")
	}
	return &Relay{
		client:    p.Client,
		publisher: pub,
		cfg:       outboxCfg,
		notify:    make(chan struct{}, 1),
	}
}

func pickPublisher(pubs []EventPublisher) EventPublisher {
	var pub EventPublisher
	for _, p := range pubs {
		if p != nil {
			pub = p
		}
	}
	return pub
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

// Notify wakes up the relay polling loop immediately to process new events without waiting for the ticker.
func (r *Relay) Notify() {
	if r == nil {
		return
	}
	select {
	case r.notify <- struct{}{}:
	default:
	}
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

// Start begins the polling loop in a background goroutine.
// It sets up cancellation and waitgroup before returning, avoiding goroutine leaks on fast shutdown.
func (r *Relay) Start(ctx context.Context) error {
	if r == nil {
		return nil
	}
	interval := r.cfg.PollInterval
	if interval <= 0 {
		return fmt.Errorf("outbox: poll_interval must be > 0")
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.wg.Add(1)
	r.mu.Unlock()

	go func() {
		defer r.wg.Done()
		r.runLoop(runCtx, interval)
	}()
	return nil
}

// Run polls synchronously until Stop is called or ctx is cancelled.
func (r *Relay) Run(ctx context.Context) {
	if r == nil {
		return
	}
	interval := r.cfg.PollInterval
	if interval <= 0 {
		slog.Error("outbox: poll_interval must be > 0")
		return
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.wg.Add(1)
	r.mu.Unlock()
	defer r.wg.Done()

	r.runLoop(runCtx, interval)
}

func (r *Relay) runLoop(runCtx context.Context, interval time.Duration) {
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
		case <-r.notify:
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
