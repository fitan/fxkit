package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"go.opentelemetry.io/otel/codes"
)

// EventPublisher 把原始 payload 字节投递到 topic（Hatchet event key = topic 名）。
// 通过 [ProvidePublisher] 注入实现（当 hatchet.outbox_publisher 为 true 时
// Hatchet 由 hatchetx 提供）。最后一个非 nil 的 publisher 生效。
type EventPublisher interface {
	PublishEvent(ctx context.Context, pubsubName, topicName string, data []byte, metadata map[string]string) error
}

// ProvidePublisher 把一个 [EventPublisher] 发布进 outbox_publishers fx group。
// 用它驱动 relay 使用 Kafka / NATS / RabbitMQ，替代（或接在）Hatchet 之后。
func ProvidePublisher(fn any) di.Option {
	return func(i di.Injector) {
		switch f := fn.(type) {
		case EventPublisher:
			di.ProvideGroupItem[EventPublisher](i, f)
		case func() EventPublisher:
			di.ProvideGroupItem[EventPublisher](i, f())
		case func(di.Injector) EventPublisher:
			di.ProvideGroupItem[EventPublisher](i, f(i))
		}
	}
}

// Relay 轮询 outbox 表并投递 pending 事件。
type Relay struct {
	client    *gormx.Client
	publisher EventPublisher
	cfg       Config

	// pubMu 只保护 publisher。与下面生命周期用的 mu 分开：
	// 前者是低频读写，后者保护 start/stop 竞态。
	pubMu sync.RWMutex

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
	notify chan struct{}
}

// NewRelayParams 是 [NewRelay] 的 Fx 友好构造函数入参。
type NewRelayParams struct {
	Client     *gormx.Client
	Outbox     *Config
	Publishers []EventPublisher
}

// NewRelay 构造 Relay。使用 fx group 中最后一个非 nil 的 [EventPublisher]。
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

// SetPublisher 替换 publisher。仅用于测试/装配完成之前；
// 运行中修改会让当前批次与后续批次用不同的 publisher。
func (r *Relay) SetPublisher(pub EventPublisher) {
	if r == nil {
		return
	}
	r.pubMu.Lock()
	r.publisher = pub
	r.pubMu.Unlock()
}

// Publisher 返回当前 publisher（可能为 nil）。
func (r *Relay) Publisher() EventPublisher {
	if r == nil {
		return nil
	}
	r.pubMu.RLock()
	defer r.pubMu.RUnlock()
	return r.publisher
}

func (r *Relay) getPublisher() EventPublisher { return r.Publisher() }

// Notify 立即唤醒 relay 轮询循环处理新事件，不必等待 ticker。
func (r *Relay) Notify() {
	if r == nil {
		return
	}
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

// batchResult 是一次 [Relay.processBatch] 调用的结果。
type batchResult struct {
	Published int
	Claimed   int
	// Failed 是本次「领取后投递失败」的条数。用于判断能否立刻继续 drain：
	// 只有真正有进展（成功发布过）才值得立刻进入下一轮，
	// 否则会形成「批量领取 → 全部投递失败 → 立刻重领」的 CPU 热循环。
	Failed int
}

// ProcessBatch 认领并投递最多 batch_size 条 pending（或陈旧 processing）事件。
// 返回的 int 是被标记为 published 的行数。
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
	recordBatch(ctx, len(events))

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

	concurrency := r.cfg.PublishConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(events) {
		concurrency = len(events)
	}

	if concurrency == 1 {
		var markErrs []error
		for _, ev := range events {
			o := r.publishOne(ctx, pub, ev, maxRetries)
			if o.published {
				out.Published++
			}
			if o.failed {
				out.Failed++
			}
			if o.markErr != nil {
				markErrs = append(markErrs, o.markErr)
			}
		}
		return out, errors.Join(markErrs...)
	}

	// 可选并发：默认 1 保持旧行为；>1 时不保证同一批内的投递顺序。
	outcomes := make([]publishOutcome, len(events))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range events {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			outcomes[i] = r.publishOne(ctx, pub, events[i], maxRetries)
		}(i)
	}
	wg.Wait()
	var markErrs []error
	for _, o := range outcomes {
		if o.published {
			out.Published++
		}
		if o.failed {
			out.Failed++
		}
		if o.markErr != nil {
			markErrs = append(markErrs, o.markErr)
		}
	}
	return out, errors.Join(markErrs...)
}

type publishOutcome struct {
	published bool
	failed    bool
	// markErr 非 nil 表示消息已投递、但状态未能标记成功（会在租约过期后重投）。
	markErr error
}

// publishOne 投递单条事件并更新它的状态（失败则按重试策略回到 pending / failed）。
func (r *Relay) publishOne(ctx context.Context, pub EventPublisher, ev OutboxEvent, maxRetries int) publishOutcome {
	out := publishOutcome{}
	ctx, span := startPublishSpan(ctx, ev.Topic)
	defer span.End()

	meta := publishMetadata(ctx, ev)
	start := time.Now()
	pubErr := pub.PublishEvent(ctx, ev.Pubsub, ev.Topic, ev.Payload, meta)
	recordPublish(ctx, time.Since(start), pubErr == nil)
	if pubErr != nil {
		span.RecordError(pubErr)
		span.SetStatus(codes.Error, "publish failed")
		n, markErr := r.markFailed(ctx, ev, maxRetries, pubErr)
		if markErr != nil {
			slog.ErrorContext(ctx, "outbox: markFailed after publish error",
				"id", ev.ID, "error", markErr,
			)
			out.failed = true
			return out
		}
		if n == 0 {
			slog.InfoContext(ctx, "outbox: markFailed skipped (lease lost)",
				"id", ev.ID, "lease_id", ev.LeaseID,
			)
			out.failed = true
			return out
		}
		slog.WarnContext(ctx, "outbox: publish failed, will retry",
			"id", ev.ID, "topic", ev.Topic, "attempts", ev.Attempts, "error", pubErr,
		)
		out.failed = true
		return out
	}
	n, err := r.markPublished(ctx, ev)
	if err != nil {
		// 已投递但未能标记：租约过期后会被重新认领并再次投递。
		// 早期实现在这里只记日志并返回成功，调用方看到的是「批次成功、
		// published 少一条」，监控上无法区分。现在向上传递。
		slog.ErrorContext(ctx, "outbox: markPublished failed (may republish after claim timeout)",
			"id", ev.ID, "error", err,
		)
		out.markErr = fmt.Errorf("outbox: mark published %d: %w", ev.ID, err)
		return out
	}
	if n == 0 {
		slog.InfoContext(ctx, "outbox: markPublished skipped (lease lost)",
			"id", ev.ID, "lease_id", ev.LeaseID,
		)
		return out
	}
	out.published = true
	if !ev.CreatedAt.IsZero() {
		recordLag(ctx, time.Since(ev.CreatedAt))
	}
	slog.InfoContext(ctx, "outbox: published",
		"id", ev.ID, "topic", ev.Topic, "pubsub", ev.Pubsub,
		"idempotency_key", idempotencyKeyValue(ev),
	)
	return out
}

func publishMetadata(ctx context.Context, ev OutboxEvent) map[string]string {
	meta := make(map[string]string, 2)
	if key := idempotencyKeyValue(ev); key != "" {
		meta[MetadataIdempotencyKey] = key
	}
	// 传播 W3C traceparent，消费端可 opt-in 续接链路。
	injectTraceContext(ctx, meta)
	if len(meta) == 0 {
		return nil
	}
	return meta
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
	// ev.Attempts 在 claim 时已自增，因此它是「含本次在内的总尝试次数」。
	// max_retries 语义为总尝试次数上限：0 = 只试一次就落 failed，N = 最多 N 次尝试。
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

// Start 在后台 goroutine 中启动轮询循环。
// 它在返回前就设置好取消与 waitgroup，避免快速关闭时 goroutine 泄漏。
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

// Run 同步轮询，直到调用 Stop 或 ctx 被取消。
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
		// 满批 **且确实有发布进展** 才立刻 drain，避免「全部失败 → 立刻重领」的
		// 热循环：失败行会被放回 pending，下一轮又被领走，CPU 会被打满。
		if err == nil && res.Published > 0 && res.Claimed >= r.cfg.BatchSize && res.Failed == 0 {
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

// Stop 取消 Run 循环并等待其退出。
func (r *Relay) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}
