package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/gormx"
	"github.com/tidwall/gjson"
	"go.opentelemetry.io/otel/codes"
)

// EventPublisher 把原始 payload 字节投递到 topic（Hatchet event key = topic 名）。
// 通过 [ProvidePublisher] 注入实现（当 hatchet.outbox_publisher 为 true 时
// Hatchet 由 hatchetx 提供）。最后一个非 nil 的 publisher 生效。
type EventPublisher interface {
	// PublishEvent 投递一条消息；返回 nil 后 relay 会把该事件标记为 published。
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
	// client 是 outbox 表所在的数据库客户端。
	client *gormx.Client
	// publisher 是当前生效的投递实现，由 pubMu 保护。
	publisher EventPublisher
	// cfg 是 relay 使用的配置副本（[NewRelay] 时拷贝）。
	cfg Config

	// pubMu 只保护 publisher。与下面生命周期用的 mu 分开：
	// 前者是低频读写，后者保护 start/stop 竞态。
	pubMu sync.RWMutex

	// mu 保护 cancel 与 wg，避免 Start/Stop 竞态。
	mu sync.Mutex
	// cancel 是轮询循环的取消函数；nil 表示未启动。
	cancel context.CancelFunc
	// wg 等待轮询 goroutine 退出。
	wg sync.WaitGroup
	// notify 用于 [Relay.Notify] 非阻塞地唤醒轮询循环，容量为 1。
	notify chan struct{}
}

// NewRelayParams 是 [NewRelay] 的 Fx 友好构造函数入参。
type NewRelayParams struct {
	// Client 是 outbox 表的数据库客户端。
	Client *gormx.Client
	// Outbox 是 outbox 配置；nil 时使用 [Config] 零值。
	Outbox *Config
	// Publishers 是 fx group 中的候选 publisher；[NewRelay] 取最后一个非 nil 的。
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

// pickPublisher 返回列表中最后一个非 nil 的 publisher。
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

// Notify 非阻塞地唤醒轮询循环立即处理一批（供写入方触发）。
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
	// Published 是本次被标记为 published 的条数。
	Published int
	// Claimed 是本次认领的条数。
	Claimed int
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

// processBatch 认领一批事件并按配置串行、并发或按 partition_field 分组投递。
// 返回本次的发布/认领/失败计数；状态回写错误会合并返回。
func (r *Relay) processBatch(ctx context.Context) (batchResult, error) {
	if r == nil || r.client == nil || !r.client.Enabled() {
		return batchResult{}, fmt.Errorf("outbox: relay has no configured database")
	}
	pub := r.Publisher()
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
	recordClaim(ctx, err)
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
		r.heartbeat(hbCtx, events[0].LeaseID, claimTimeout)
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

	// 分组必须先于「并发度为 1」的捷径。PublishConcurrency 默认就是 1，
	// 若先走串行循环，partition_field 永远不会生效，失败后同 key 的后续事件仍会被发出。
	if field := strings.TrimSpace(r.cfg.PartitionField); field != "" {
		return r.processPartitioned(ctx, pub, events, field, concurrency, maxRetries, &out)
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

	// 未分组：全局并发，不保证同一批内的投递顺序。
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

// publishOutcome 是 [Relay.publishOne] 对单条事件的投递结果。
type publishOutcome struct {
	// published 为 true 表示已投递并成功标记为 published。
	published bool
	// failed 为 true 表示本次投递失败（已按重试策略回写状态）。
	failed bool
	// markErr 非 nil 表示消息已投递、但状态未能标记成功（会在租约过期后重投）。
	markErr error
}

// processPartitioned 按 payload 中的 field 分组投递：
// 同一分组内严格串行（保持产生顺序），不同分组由最多 concurrency 个 worker 并行。
//
// 顺序只保证单个 relay 的同一批：同 key 按 id 顺序投递，当前事件没成功就停住该组。
// 跨批重试不保证同 key 顺序（组头在退避，组尾租约到期后可能先发）。
// 多副本用 SKIP LOCKED 领走更大的 id，同样不保证全序。没设分组时，失败不挡住同批后续事件。
func (r *Relay) processPartitioned(
	ctx context.Context,
	pub EventPublisher,
	events []OutboxEvent,
	field string,
	concurrency int,
	maxRetries int,
	out *batchResult,
) (batchResult, error) {
	// 分组（保持组内相对顺序）。
	order := make([]string, 0, len(events))
	groups := make(map[string][]OutboxEvent, len(events))
	for _, ev := range events {
		key := partitionKeyOf(ev, field)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], ev)
	}

	if len(groups) > 0 && concurrency > len(groups) {
		concurrency = len(groups)
	}

	type groupResult struct {
		published int
		failed    int
		markErrs  []error
	}
	results := make([]groupResult, len(order))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, key := range order {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, evs []OutboxEvent) {
			defer wg.Done()
			defer func() { <-sem }()
			var res groupResult
			for _, ev := range evs {
				o := r.publishOne(ctx, pub, ev, maxRetries)
				if o.published {
					res.published++
				}
				if o.failed {
					res.failed++
				}
				if o.markErr != nil {
					res.markErrs = append(res.markErrs, o.markErr)
				}
				// 同 key 保序：当前事件未成功（失败或状态未落库）时必须停止本组，
				// 否则后续事件会先于它被消费，正好在重试场景下破坏顺序。
				// 未处理的事件仍处于 processing，租约到期后会被重新认领。
				if !o.published || o.markErr != nil {
					break
				}
			}
			results[i] = res
		}(i, groups[key])
	}
	wg.Wait()

	var markErrs []error
	for _, res := range results {
		out.Published += res.published
		out.Failed += res.failed
		markErrs = append(markErrs, res.markErrs...)
	}
	return *out, errors.Join(markErrs...)
}

// partitionKeyOf 取 payload 中 field 的值作为分组键。
// 取不到时返回空串（这些事件会归入同一串行组，宁可牺牲并发也不打乱顺序）。
func partitionKeyOf(ev OutboxEvent, field string) string {
	return strings.TrimSpace(gjson.GetBytes(ev.Payload, field).String())
}

// publishOne 投递单条事件并更新它的状态（失败则按重试策略回到 pending / failed）。
func (r *Relay) publishOne(ctx context.Context, pub EventPublisher, ev OutboxEvent, maxRetries int) publishOutcome {
	out := publishOutcome{}
	ctx, span := startPublishSpan(ctx, ev.Topic)
	defer span.End()

	meta := publishMetadata(ctx, ev)
	start := time.Now()
	pubCtx := ctx
	if r.cfg.PublishTimeout > 0 {
		var cancel context.CancelFunc
		pubCtx, cancel = context.WithTimeout(ctx, r.cfg.PublishTimeout)
		defer cancel()
	}
	pubErr := pub.PublishEvent(pubCtx, ev.Pubsub, ev.Topic, ev.Payload, meta)
	recordPublish(ctx, time.Since(start), pubErr == nil)
	// 状态回写不能跟着批次 ctx 一起被取消（优雅停机时批次 ctx 已 cancel），
	// 否则已投递/已失败的事件会停在 processing，租约到期后被重复投递。
	markCtx, markCancel := terminalWriteContext(ctx)
	defer markCancel()
	if pubErr != nil {
		span.RecordError(pubErr)
		span.SetStatus(codes.Error, "publish failed")
		n, markErr := r.markFailed(markCtx, ev, maxRetries, pubErr)
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
			"id", ev.ID, "topic", ev.Topic, "attempts", ev.Attempts+1, "error", pubErr,
		)
		out.failed = true
		return out
	}
	n, err := r.markPublished(markCtx, ev)
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

// publishMetadata 组装投递元数据：幂等键与 W3C traceparent；为空时返回 nil。
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

// idempotencyKeyValue 返回事件的幂等键，未设置时返回空串。
func idempotencyKeyValue(ev OutboxEvent) string {
	if ev.IdempotencyKey == nil {
		return ""
	}
	return *ev.IdempotencyKey
}

// terminalWriteContext 返回用于写终态（published/pending/failed）的 context。
// 它脱离调用方的取消信号，保证优雅停机时仍能写完状态；同时保留硬超时，
// 避免数据库挂起时无限阻塞。
func terminalWriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

// heartbeat 在批次处理期间周期刷新租约行的 locked_at，避免被其他副本重新认领。
// interval 取 claimTimeout/3（下限 1s），ctx 结束时退出。
func (r *Relay) heartbeat(ctx context.Context, leaseID string, claimTimeout time.Duration) {
	if leaseID == "" {
		return
	}
	interval := claimTimeout / 3
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
			err := r.client.Conn(ctx).Model(&OutboxEvent{}).
				Where("lease_id = ? AND status = ?", leaseID, StatusProcessing).
				Update("locked_at", now).Error
			if errors.Is(err, context.Canceled) {
				continue
			}
			recordHeartbeat(ctx, err)
			if err != nil {
				slog.WarnContext(ctx, "outbox: lease heartbeat failed",
					"lease_id", leaseID, "error", err,
				)
			}
		}
	}
}

// markPublished 以 lease 为 CAS 条件把事件标记为 published；返回受影响行数。
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

// markFailed 以 lease 为 CAS 条件递增 attempts，并按 maxRetries 决定回到 pending 还是 failed，
// 同时写入 last_error 与下次重试时间；返回受影响行数。
func (r *Relay) markFailed(ctx context.Context, ev OutboxEvent, maxRetries int, pubErr error) (int64, error) {
	// attempts 只在真正投递失败时递增：租约过期被重新认领不算一次业务重试。
	// max_retries 仍是「总失败次数上限」：0 = 第一次失败就落 failed。
	// 用 CASE 在一条 UPDATE 里完成「递增 + 判定终态 + 计算下次重试时间」，
	// 避免读改写竞态。
	//
	// 列顺序必须写死。GORM 对 map 的 key 按字母序生成 SET，attempts 会排在
	// 两个 CASE 前面；MySQL 从左到右赋值，CASE 会看到已经 +1 的 attempts，
	// 于是 max_retries=N 在第 N-1 次失败就标成 failed。
	next := r.nextAttemptAt(ev.Attempts + 1)
	var nextAt any
	if next != nil {
		nextAt = *next
	}
	// PostgreSQL 在 THEN NULL ELSE ? 上推断不出参数类型（尤其 ? 是 NULL 时），
	// 会把表达式当成 text，写不进 timestamptz。显式 CAST 把类型定死。
	// MySQL / SQLite 不需要，CAST AS timestamptz 在 SQLite 里会把时间弄成数字。
	nextExpr := "?"
	if r.client.Conn(ctx).Dialector.Name() == "postgres" {
		nextExpr = "CAST(? AS timestamptz)"
	}
	res := r.client.Conn(ctx).Exec(
		`UPDATE outbox_events SET
			status = CASE WHEN attempts + 1 >= ? THEN ? ELSE ? END,
			next_attempt_at = CASE WHEN attempts + 1 >= ? THEN NULL ELSE `+nextExpr+` END,
			attempts = attempts + 1,
			last_error = ?,
			locked_at = NULL,
			lease_id = ?
		WHERE id = ? AND lease_id = ? AND status = ?`,
		maxRetries, StatusFailed, StatusPending,
		maxRetries, nextAt,
		pubErr.Error(),
		"",
		ev.ID, ev.LeaseID, StatusProcessing,
	)
	return res.RowsAffected, res.Error
}

// nextAttemptAt 按失败次数计算下次可重试时间。
// attempt 从 1 开始（第一次失败）；RetryBase<=0 表示不退避。
func (r *Relay) nextAttemptAt(attempt int) *time.Time {
	base := r.cfg.RetryBase
	if base <= 0 {
		return nil
	}
	max := r.cfg.RetryMax
	if max <= 0 {
		max = base
	}
	d := base
	for i := 1; i < attempt && d < max; i++ {
		d *= 2
		if d > max {
			d = max
		}
	}
	if p := r.cfg.RetryJitterPercent; p > 0 && d > 0 {
		delta := int64(d) * int64(p) / 100
		if delta > 0 {
			d += time.Duration(rand.Int64N(delta*2+1) - delta)
		}
	}
	if d <= 0 {
		return nil
	}
	t := time.Now().UTC().Add(d)
	return &t
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

// runLoop 周期执行 processBatch，直到 runCtx 结束或收到 Notify。
// 满批且确有发布进展时才立即继续，避免全部失败时的热循环。
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
	// 置空后 Start/Run 可以再次启动。不置空会让「重启」静默变成 no-op。
	// 必须放在 wg.Wait 之后：否则新的 Start 可能与旧循环并发。
	r.mu.Lock()
	r.cancel = nil
	r.mu.Unlock()
}
