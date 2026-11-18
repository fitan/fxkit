package payments

import (
	"time"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

// CreatePaymentReq 是创建支付单的请求；金额使用最小货币单位。
type CreatePaymentReq struct {
	// Amount 是支付金额（最小货币单位）。
	Amount int64 `json:"amount"`
	// Currency 是 ISO 4217 币种，如 CNY。
	Currency string `json:"currency"`
	// Provider 可选；为空时用配置默认渠道。
	Provider string `json:"provider,omitempty"`
}

// PaymentDetail 是支付单响应。
type PaymentDetail struct {
	// ID 是内部支付 ID。
	ID string `json:"id"`
	// Amount 是支付金额。
	Amount int64 `json:"amount"`
	// CapturedAmount 是已捕获金额。
	CapturedAmount int64 `json:"capturedAmount"`
	// RefundedAmount 是累计退款金额。
	RefundedAmount int64 `json:"refundedAmount"`
	// Currency 是币种。
	Currency string `json:"currency"`
	// Status 是当前支付状态。
	Status string `json:"status"`
	// Version 是乐观锁版本。
	Version int64 `json:"version"`
	// Provider 是渠道标识。
	Provider string `json:"provider,omitempty"`
	// ProviderRef 是渠道引用。
	ProviderRef string `json:"providerRef,omitempty"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 是更新时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// RefundReq 是退款请求；Amount 省略或为 0 时退剩余可退金额。
type RefundReq struct {
	// Amount 是本次退款金额；0/省略 = 剩余全部。
	Amount int64 `json:"amount,omitempty"`
}

// WebhookEvent 是渠道回调的业务事件（验签后使用）。
type WebhookEvent struct {
	// EventID 是渠道事件 ID，用于 inbox 去重。
	EventID string `json:"eventId"`
	// PaymentID 是内部支付 ID。
	PaymentID string `json:"paymentId"`
	// Type 是事件类型（authorized/captured/refunded/...）。
	Type string `json:"type"`
	// Amount 是渠道声明的支付金额，用于与内部记录比对。
	Amount int64 `json:"amount"`
	// Currency 是币种。
	Currency string `json:"currency"`
}

// SettlementRow 是对账用的内部/外部结算行，以 PaymentID 对齐。
type SettlementRow struct {
	// PaymentID 是支付单 ID。
	PaymentID string `json:"paymentId"`
	// Amount 是结算金额。
	Amount int64 `json:"amount"`
	// Currency 是币种。
	Currency string `json:"currency"`
}

// LedgerEntry 是账本分录的接口投影。
type LedgerEntry struct {
	// TxnID 是账本事务 ID。
	TxnID string `json:"txnId"`
	// AccountID 是账户。
	AccountID string `json:"accountId"`
	// Amount 是有符号金额。
	Amount int64 `json:"amount"`
	// Currency 是币种。
	Currency string `json:"currency"`
	// CreatedAt 是记账时间。
	CreatedAt time.Time `json:"createdAt"`
}

// ReconcileDiff 是对账差异：missing_external / missing_internal / mismatch。
type ReconcileDiff struct {
	// Key 是对账键（支付单 ID）。
	Key string `json:"key"`
	// Kind 是差异类型。
	Kind string `json:"kind"`
	// Internal 是内部侧记录。
	Internal *SettlementRow `json:"internal,omitempty"`
	// External 是渠道侧记录。
	External *SettlementRow `json:"external,omitempty"`
}

// reconcileBody 是对账接口响应，Summary 按差异类型计数。
type reconcileBody struct {
	// Diffs 是差异明细。
	Diffs []ReconcileDiff `json:"diffs"`
	// Summary 是按类型统计的差异数量。
	Summary map[string]int `json:"summary"`
}

// PaymentEvent 是 outbox / Hatchet 的支付事件负载。
type PaymentEvent struct {
	// PaymentID 是支付单 ID。
	PaymentID string `json:"paymentId"`
	// Type 是事件类型。
	Type string `json:"type"`
	// Amount 是支付金额（非退款金额）。
	Amount int64 `json:"amount"`
	// Currency 是币种。
	Currency string `json:"currency"`
	// Version 是支付单版本，用于识别重复/乱序。
	Version int64 `json:"version"`
	// RefundID 是退款事件对应的退款单 ID。
	RefundID string `json:"refundId,omitempty"`
	// RefundAmount 是本次退款金额。
	RefundAmount int64 `json:"refundAmount,omitempty"`
}

// toDetail 把支付单实体转为接口模型。
func toDetail(p entity.Payment) PaymentDetail {
	return PaymentDetail{
		ID:             p.ID,
		Amount:         p.Amount,
		CapturedAmount: p.CapturedAmount,
		RefundedAmount: p.RefundedAmount,
		Currency:       p.Currency,
		Status:         p.State,
		Version:        p.Version,
		Provider:       p.Provider,
		ProviderRef:    p.ProviderRef,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
	}
}
