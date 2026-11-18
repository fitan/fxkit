package payments

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"go.jetify.com/typeid"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/ledger"
	"github.com/fitan/fxkit/money"
	"github.com/fitan/fxkit/outbox"
	"github.com/fitan/fxkit/payment"
	"github.com/fitan/fxkit/reconciliation"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
	"github.com/fitan/fxkit-example/services/catalog/internal/repository"
)

// Config 是 payments 段配置：默认渠道与 webhook 验签密钥。
type Config struct {
	// Provider 是默认渠道标识。
	Provider string `yaml:"provider"`
	// WebhookSecret 是渠道回调 HMAC 密钥。
	WebhookSecret string `yaml:"webhook_secret"`
}

// SetDefaults 提供本地开发默认值；生产必须覆盖 webhook_secret。
func (c *Config) SetDefaults() {
	c.Provider = "mock"
	c.WebhookSecret = "dev-webhook-secret"
}

// Validate 拒绝空的 webhook_secret：没有密钥就无法安全验签。
func (c Config) Validate() error {
	if strings.TrimSpace(c.WebhookSecret) == "" {
		return fmt.Errorf("payments.webhook_secret is required")
	}
	return nil
}

// Service 是支付用例层，串起状态机、账本、outbox 与渠道。
// 渠道网络调用放在 DB 事务之外；事务只覆盖状态变更 + 记账 + 事件。
type Service struct {
	db       *gormx.Client
	repo     *repository.PaymentRepo
	ledger   *ledger.Ledger
	store    *outbox.Store
	inbox    *outbox.Inbox
	outbox   *outbox.Config
	provider Provider
	cfg      *Config
	machine  *payment.Machine
}

// NewService 装配支付用例依赖，并迁移 payments / inbox 表。
// machine 显式列出允许的状态迁移，未列出的转换一律 409。
func NewService(
	db *gormx.Client,
	repo *repository.PaymentRepo,
	l *ledger.Ledger,
	store *outbox.Store,
	inbox *outbox.Inbox,
	obcfg *outbox.Config,
	provider Provider,
	cfg *Config,
	lc *di.Lifecycle,
) (*Service, error) {
	if db == nil {
		return nil, fxerrors.Internal("payments: nil gormx client")
	}
	if repo == nil || l == nil {
		return nil, fxerrors.Internal("payments: nil repository or ledger")
	}
	if provider == nil {
		provider = NewMockProvider()
	}
	if cfg == nil {
		cfg = &Config{}
		cfg.SetDefaults()
	}
	s := &Service{
		db:       db,
		repo:     repo,
		ledger:   l,
		store:    store,
		inbox:    inbox,
		outbox:   obcfg,
		provider: provider,
		cfg:      cfg,
		machine: payment.NewMachine(
			payment.Transition{From: "created", To: "authorized"},
			payment.Transition{From: "created", To: "failed"},
			payment.Transition{From: "authorized", To: "captured"},
			payment.Transition{From: "authorized", To: "failed"},
			payment.Transition{From: "authorized", To: "canceled"},
			payment.Transition{From: "captured", To: "refunded"},
			payment.Transition{From: "captured", To: "settled"},
		),
	}
	if lc != nil {
		lc.Append(di.Hook{
			OnStart: func(ctx context.Context) error {
				if err := entity.MigratePayments(db.Conn(ctx)); err != nil {
					return err
				}
				if inbox != nil {
					return inbox.Migrate(ctx)
				}
				return nil
			},
		})
	}
	return s, nil
}

// Create 创建支付单：金额/币种校验 → 落库 → 写 outbox。
// 返回 201 之前不调用渠道；授权/扣款是后续独立步骤。
func (s *Service) Create(ctx context.Context, req CreatePaymentReq) (PaymentDetail, error) {
	// money 只接受整数最小单位，并在构造函数里校验币种。
	m, err := money.New(req.Amount, req.Currency)
	if err != nil {
		return PaymentDetail{}, fxerrors.Validation(map[string]string{"amount/currency": err.Error()})
	}
	// 支付金额必须为正；退款/调账走其它用例。
	if m.Amount <= 0 {
		return PaymentDetail{}, fxerrors.Validation(map[string]string{"amount": "must be > 0"})
	}
	// 内部支付 ID 由服务端生成，同时作为渠道幂等键的稳定来源。
	id, err := newID()
	if err != nil {
		return PaymentDetail{}, fxerrors.Internal("payments: generate id: %v", err)
	}
	// 允许单笔指定渠道；未指定时用配置默认值。
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = s.cfg.Provider
	}
	p := entity.Payment{
		ID:       id,
		Amount:   m.Amount,
		Currency: string(m.Currency),
		State:    "created",
		Version:  1,
		Provider: provider,
	}
	_, err = s.db.WithTxResult(ctx, func(txCtx context.Context) (struct{}, error) {
		// 支付单与 payment-created 事件同一个事务：要么都成功，要么都回滚。
		if err := s.repo.Insert(txCtx, &p); err != nil {
			return struct{}{}, err
		}
		// 只在事务真正提交后执行；回滚时不会运行。
		gormx.AfterCommit(txCtx, func(context.Context) {
			slog.Info("payment created after commit", "payment_id", p.ID)
		})
		return struct{}{}, s.enqueue(txCtx, "payment-created", "payment-created:"+p.ID, eventOf(p, "created"))
	})
	if err != nil {
		return PaymentDetail{}, err
	}
	return toDetail(p), nil
}

// List 返回最近的支付单（默认 50，最大 200）。
func (s *Service) List(ctx context.Context, limit int) ([]PaymentDetail, error) {
	rows, err := s.repo.ListRecent(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]PaymentDetail, 0, len(rows))
	for _, p := range rows {
		out = append(out, toDetail(p))
	}
	return out, nil
}

// Get 按内部支付 ID 读取。
func (s *Service) Get(ctx context.Context, id string) (PaymentDetail, error) {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return PaymentDetail{}, err
	}
	return toDetail(p), nil
}

// Authorize 调用渠道授权，成功后用 CAS 把状态改为 authorized。
// 渠道调用在事务外；事务内重新读取最新状态再 Apply，避免覆盖并发变更。
func (s *Service) Authorize(ctx context.Context, id string) (PaymentDetail, error) {
	// 先做便宜的本地状态判断，非法状态不必打渠道。
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return PaymentDetail{}, err
	}
	if p.State != "created" {
		return PaymentDetail{}, fxerrors.Conflict("payments: invalid state %s for authorize", p.State)
	}
	// 渠道幂等键直接复用内部支付 ID：重试不会重复授权。
	ref, err := s.provider.Authorize(ctx, p, p.ID)
	if err != nil {
		return PaymentDetail{}, fxerrors.Unavailable("payments: provider authorize: %v", err)
	}
	_, err = s.db.WithTxResult(ctx, func(txCtx context.Context) (struct{}, error) {
		cur, err := s.repo.Get(txCtx, id)
		if err != nil {
			return struct{}{}, err
		}
		if err := payment.Apply(txCtx, s.db, s.machine, &cur, payment.State("authorized")); err != nil {
			return struct{}{}, err
		}
		if err := s.db.Conn(txCtx).Model(&cur).Update("provider_ref", ref).Error; err != nil {
			return struct{}{}, fxerrors.Wrap(err)
		}
		cur.ProviderRef = ref
		return struct{}{}, s.enqueue(txCtx, "payment-authorized",
			fmt.Sprintf("payment:%s:authorized:v%d", cur.ID, cur.Version), eventOf(cur, "authorized"))
	})
	if err != nil {
		return PaymentDetail{}, err
	}
	return s.Get(ctx, id)
}

// Capture 扣款：状态 CAS + 复式记账 + outbox 在同一事务。
func (s *Service) Capture(ctx context.Context, id string) (PaymentDetail, error) {
	// 预检查：只有 authorized 的支付单可以扣款。
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return PaymentDetail{}, err
	}
	if p.State != "authorized" {
		return PaymentDetail{}, fxerrors.Conflict("payments: invalid state %s for capture", p.State)
	}
	if err := s.provider.Capture(ctx, p, p.ID); err != nil {
		return PaymentDetail{}, fxerrors.Unavailable("payments: provider capture: %v", err)
	}
	_, err = s.db.WithTxResult(ctx, func(txCtx context.Context) (struct{}, error) {
		cur, err := s.repo.Get(txCtx, id)
		if err != nil {
			return struct{}{}, err
		}
		if err := payment.Apply(txCtx, s.db, s.machine, &cur, payment.State("captured")); err != nil {
			return struct{}{}, err
		}
		// 记录已捕获金额，供后续部分退款计算剩余额度。
		if err := s.db.Conn(txCtx).Model(&cur).Update("captured_amount", cur.Amount).Error; err != nil {
			return struct{}{}, fxerrors.Wrap(err)
		}
		cur.CapturedAmount = cur.Amount
		if err := s.postCapture(txCtx, cur); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, s.enqueue(txCtx, "payment-captured",
			fmt.Sprintf("payment:%s:captured:v%d", cur.ID, cur.Version), eventOf(cur, "captured"))
	})
	if err != nil {
		return PaymentDetail{}, err
	}
	return s.Get(ctx, id)
}

// Refund 退款：支持多次部分退款。Amount 省略/为 0 时退剩余可退金额。
func (s *Service) Refund(ctx context.Context, id string, req RefundReq) (PaymentDetail, error) {
	// 预检查：只有 captured 的支付单可以退款。
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return PaymentDetail{}, err
	}
	if p.State != "captured" {
		return PaymentDetail{}, fxerrors.Conflict("payments: invalid state %s for refund", p.State)
	}
	captured := capturedAmount(p)
	remaining := captured - p.RefundedAmount
	if remaining <= 0 {
		return PaymentDetail{}, fxerrors.Conflict("payments: nothing left to refund")
	}
	amount := req.Amount
	if amount <= 0 {
		amount = remaining
	}
	if amount > remaining {
		return PaymentDetail{}, fxerrors.Validation(map[string]string{
			"amount": fmt.Sprintf("must be between 1 and %d", remaining),
		})
	}
	refundID, err := newRefundID()
	if err != nil {
		return PaymentDetail{}, fxerrors.Internal("payments: generate refund id: %v", err)
	}
	// 渠道幂等键用退款 ID：每次退款独立、重试复用。
	if err := s.provider.Refund(ctx, p, amount, refundID); err != nil {
		return PaymentDetail{}, fxerrors.Unavailable("payments: provider refund: %v", err)
	}
	_, err = s.db.WithTxResult(ctx, func(txCtx context.Context) (struct{}, error) {
		cur, err := s.repo.Get(txCtx, id)
		if err != nil {
			return struct{}{}, err
		}
		if cur.State != "captured" {
			return struct{}{}, fxerrors.Conflict("payments: invalid state %s for refund", cur.State)
		}
		curCaptured := capturedAmount(cur)
		curRemaining := curCaptured - cur.RefundedAmount
		if amount > curRemaining {
			return struct{}{}, fxerrors.Conflict("payments: refund exceeds remaining amount")
		}
		refund := entity.Refund{
			ID:        refundID,
			PaymentID: id,
			Amount:    amount,
			Currency:  cur.Currency,
			State:     "succeeded",
		}
		if err := s.repo.InsertRefund(txCtx, &refund); err != nil {
			return struct{}{}, err
		}
		newRefunded := cur.RefundedAmount + amount
		if newRefunded >= curCaptured {
			// 退完：状态转 refunded，再写回累计退款额。
			if err := payment.Apply(txCtx, s.db, s.machine, &cur, payment.State("refunded")); err != nil {
				return struct{}{}, err
			}
			if err := s.db.Conn(txCtx).Model(&cur).Update("refunded_amount", newRefunded).Error; err != nil {
				return struct{}{}, fxerrors.Wrap(err)
			}
		} else {
			// 部分退款：状态保持 captured，只更新累计退款额（乐观锁）。
			cur.RefundedAmount = newRefunded
			if err := gormx.UpdateWithVersion(txCtx, s.db, &cur, "refunded_amount"); err != nil {
				return struct{}{}, err
			}
		}
		cur.RefundedAmount = newRefunded
		if err := s.postRefund(txCtx, cur, refundID, amount); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, s.enqueue(txCtx, "payment-refunded",
			fmt.Sprintf("payment:%s:refunded:%s", cur.ID, refundID),
			eventOfRefund(cur, refundID, amount))
	})
	if err != nil {
		return PaymentDetail{}, err
	}
	return s.Get(ctx, id)
}

// LedgerEntries 汇总一个支付单的扣款与每次退款分录。
func (s *Service) LedgerEntries(ctx context.Context, id string) ([]LedgerEntry, error) {
	var out []LedgerEntry
	appendTxn := func(txnID string) error {
		rows, err := s.ledger.Entries(ctx, txnID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			out = append(out, LedgerEntry{
				TxnID: row.TxnID, AccountID: row.AccountID,
				Amount: row.Amount, Currency: row.Currency, CreatedAt: row.CreatedAt,
			})
		}
		return nil
	}
	if err := appendTxn("payment:" + id + ":captured"); err != nil {
		return nil, err
	}
	// 兼容旧数据的全额退款事务 ID。
	if err := appendTxn("payment:" + id + ":refunded"); err != nil {
		return nil, err
	}
	refunds, err := s.repo.ListRefunds(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, r := range refunds {
		if err := appendTxn("payment:" + id + ":refunded:" + r.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// HandleWebhook 用 provider event ID 做消费去重，再走状态机/账本/outbox。
func (s *Service) HandleWebhook(ctx context.Context, ev WebhookEvent) (PaymentDetail, error) {
	if strings.TrimSpace(ev.EventID) == "" {
		return PaymentDetail{}, fxerrors.BadRequest("payments: eventId is required")
	}
	if ev.Amount <= 0 {
		return PaymentDetail{}, fxerrors.BadRequest("payments: amount must be > 0")
	}
	if _, err := money.New(ev.Amount, ev.Currency); err != nil {
		return PaymentDetail{}, fxerrors.Validation(map[string]string{"currency": err.Error()})
	}
	apply := func(ctx context.Context) error { return s.applyWebhook(ctx, ev) }
	if s.inbox == nil {
		if err := apply(ctx); err != nil {
			return PaymentDetail{}, err
		}
	} else if err := s.inbox.Once(ctx, outbox.OnceInput{
		Key:   "payment-webhook:" + ev.EventID,
		Topic: "payment-webhook",
		Fn:    apply,
	}); err != nil {
		return PaymentDetail{}, err
	}
	p, err := s.repo.Get(ctx, ev.PaymentID)
	if err != nil {
		return PaymentDetail{}, err
	}
	return toDetail(p), nil
}

// applyWebhook 在事务内做金额校验、状态 CAS、记账与 outbox。
// inbox.Once 已经把「同一个 provider event ID」挡在事务之外。
func (s *Service) applyWebhook(ctx context.Context, ev WebhookEvent) error {
	_, err := s.db.WithTxResult(ctx, func(txCtx context.Context) (struct{}, error) {
		p, err := s.repo.Get(txCtx, ev.PaymentID)
		if err != nil {
			return struct{}{}, err
		}
		if p.Amount != ev.Amount || p.Currency != strings.ToUpper(ev.Currency) {
			return struct{}{}, fxerrors.Conflict("payments: webhook amount/currency mismatch")
		}
		var event PaymentEvent
		switch ev.Type {
		case "captured":
			if err := payment.Apply(txCtx, s.db, s.machine, &p, payment.State("captured")); err != nil {
				return struct{}{}, err
			}
			if err := s.db.Conn(txCtx).Model(&p).Update("captured_amount", p.Amount).Error; err != nil {
				return struct{}{}, fxerrors.Wrap(err)
			}
			p.CapturedAmount = p.Amount
			if err := s.postCapture(txCtx, p); err != nil {
				return struct{}{}, err
			}
			event = eventOf(p, ev.Type)
		case "refunded":
			captured := capturedAmount(p)
			remaining := captured - p.RefundedAmount
			if remaining <= 0 {
				return struct{}{}, fxerrors.Conflict("payments: nothing left to refund")
			}
			refundID, err := newRefundID()
			if err != nil {
				return struct{}{}, fxerrors.Internal("payments: generate refund id: %v", err)
			}
			if err := s.repo.InsertRefund(txCtx, &entity.Refund{
				ID: refundID, PaymentID: p.ID, Amount: remaining,
				Currency: p.Currency, State: "succeeded",
			}); err != nil {
				return struct{}{}, err
			}
			if err := payment.Apply(txCtx, s.db, s.machine, &p, payment.State("refunded")); err != nil {
				return struct{}{}, err
			}
			if err := s.db.Conn(txCtx).Model(&p).Update("refunded_amount", captured).Error; err != nil {
				return struct{}{}, fxerrors.Wrap(err)
			}
			p.RefundedAmount = captured
			if err := s.postRefund(txCtx, p, refundID, remaining); err != nil {
				return struct{}{}, err
			}
			event = eventOfRefund(p, refundID, remaining)
		default:
			if err := payment.Apply(txCtx, s.db, s.machine, &p, payment.State(ev.Type)); err != nil {
				return struct{}{}, err
			}
			event = eventOf(p, ev.Type)
		}
		return struct{}{}, s.enqueue(txCtx, "payment-"+ev.Type,
			fmt.Sprintf("payment:%s:%s:v%d", p.ID, ev.Type, p.Version), event)
	})
	return err
}

// Reconcile 对比内部已结算支付单与渠道结算单，返回差异列表。
func (s *Service) Reconcile(ctx context.Context) ([]ReconcileDiff, error) {
	internalRows, err := s.repo.ListSettled(ctx)
	if err != nil {
		return nil, err
	}
	internal := make([]SettlementRow, 0, len(internalRows))
	for _, p := range internalRows {
		internal = append(internal, SettlementRow{PaymentID: p.ID, Amount: p.Amount, Currency: p.Currency})
	}
	external, err := s.provider.Settlements(ctx)
	if err != nil {
		return nil, err
	}
	diffs, err := reconciliation.Run(ctx, reconciliation.Comparator[SettlementRow]{
		Internal: func(context.Context) ([]SettlementRow, error) { return internal, nil },
		External: func(context.Context) ([]SettlementRow, error) { return external, nil },
		Key:      func(r SettlementRow) string { return r.PaymentID },
		Equal: func(a, b SettlementRow) bool {
			return a.Amount == b.Amount && a.Currency == b.Currency
		},
	})
	if err != nil {
		return nil, err
	}
	out := make([]ReconcileDiff, len(diffs))
	for i, d := range diffs {
		out[i] = ReconcileDiff{Key: d.Key, Kind: string(d.Kind), Internal: d.Internal, External: d.External}
	}
	return out, nil
}

// postCapture 记扣款：借 cash、贷 merchant_payable。
func (s *Service) postCapture(ctx context.Context, p entity.Payment) error {
	return s.ledger.Post(ctx, ledger.Transaction{
		ID:        "payment:" + p.ID + ":captured",
		Reference: p.ID,
	}, []ledger.Posting{
		{AccountID: "cash", Amount: p.Amount, Currency: p.Currency},
		{AccountID: "merchant_payable", Amount: -p.Amount, Currency: p.Currency},
	})
}

// postRefund 记退款：方向与扣款相反，同币种求和为 0；每次退款一个事务 ID。
func (s *Service) postRefund(ctx context.Context, p entity.Payment, refundID string, amount int64) error {
	return s.ledger.Post(ctx, ledger.Transaction{
		ID:        "payment:" + p.ID + ":refunded:" + refundID,
		Reference: p.ID,
	}, []ledger.Posting{
		{AccountID: "cash", Amount: -amount, Currency: p.Currency},
		{AccountID: "merchant_payable", Amount: amount, Currency: p.Currency},
	})
}

// enqueue 写 outbox；outbox 未启用时是空操作（示例允许无 Hatchet 运行）。
// key 必须稳定：同一个业务状态变更重试时不能换 key。
func (s *Service) enqueue(ctx context.Context, topic, key string, ev PaymentEvent) error {
	if s.outbox == nil || !s.outbox.Enabled || s.store == nil {
		return nil
	}
	return outbox.EnqueueTopicMsg(ctx, outbox.EnqueueTopicInput[PaymentEvent]{
		Store:          s.store,
		Topic:          topic,
		Msg:            ev,
		IdempotencyKey: key,
	})
}

// eventOf 构造 outbox/Hatchet 事件；Version 让消费端能识别重复或乱序。
func eventOf(p entity.Payment, typ string) PaymentEvent {
	return PaymentEvent{
		PaymentID: p.ID,
		Type:      typ,
		Amount:    p.Amount,
		Currency:  p.Currency,
		Version:   p.Version,
	}
}

// eventOfRefund 在退款事件里带上本次退款 ID 与金额。
func eventOfRefund(p entity.Payment, refundID string, amount int64) PaymentEvent {
	ev := eventOf(p, "refunded")
	ev.RefundID = refundID
	ev.RefundAmount = amount
	return ev
}

// capturedAmount 返回已捕获金额；兼容早期没有 captured_amount 列的数据。
func capturedAmount(p entity.Payment) int64 {
	if p.CapturedAmount > 0 {
		return p.CapturedAmount
	}
	switch p.State {
	case "captured", "refunded", "settled":
		return p.Amount
	}
	return 0
}

// paymentIDPrefix 是 TypeID 的类型前缀；规范要求小写字母。
const paymentIDPrefix = "pay"

// refundIDPrefix 是退款单的 TypeID 前缀。
const refundIDPrefix = "ref"

// newID 生成内部支付 ID：TypeID（pay_ + Base32 编码的 UUIDv7）。
//
// TypeID 自带类型前缀，同时底层仍是 UUIDv7，因此：
//   - 字符串按时间有序（毫秒级，同进程内单调）；
//   - 需 30 字符（pay_ + 26），比 UUID 文本短；
//   - 前缀不增加碰撞空间，碰撞概率与底层 UUIDv7 一致。
//
// 该 ID 同时作为渠道幂等键，重试时必须复用。
func newID() (string, error) {
	tid, err := typeid.WithPrefix(paymentIDPrefix)
	if err != nil {
		return "", err
	}
	return tid.String(), nil
}

// newRefundID 生成退款单 ID：ref_ + UUIDv7。
func newRefundID() (string, error) {
	tid, err := typeid.WithPrefix(refundIDPrefix)
	if err != nil {
		return "", err
	}
	return tid.String(), nil
}
