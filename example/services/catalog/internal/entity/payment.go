package entity

import (
	"time"

	"github.com/fitan/fxkit/payment"
)

// Payment 是支付单。State 列名为 state，供 payment.Apply 的 CAS 使用。
type Payment struct {
	// ID 是内部支付 ID（TypeID：pay_...）。
	ID string `gorm:"primaryKey;size:64" json:"-"`
	// Amount 是支付金额（最小货币单位）。
	Amount int64 `gorm:"not null" json:"-"`
	// Currency 是 ISO 4217 币种。
	Currency string `gorm:"size:3;not null" json:"-"`
	// State 是支付状态（created/authorized/captured/refunded/...）。
	State string `gorm:"size:32;not null;index" json:"-"`
	// Version 是乐观锁版本。
	Version int64 `gorm:"not null;default:1" json:"-"`
	// Provider 是渠道标识（如 mock）。
	Provider string `gorm:"size:32" json:"-"`
	// ProviderRef 是渠道返回的引用。
	ProviderRef string `gorm:"size:128" json:"-"`
	// CapturedAmount 是已捕获金额。
	CapturedAmount int64 `gorm:"not null;default:0" json:"-"`
	// RefundedAmount 是累计退款金额。
	RefundedAmount int64 `gorm:"not null;default:0" json:"-"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"-"`
	// UpdatedAt 是更新时间。
	UpdatedAt time.Time `json:"-"`
}

// TableName 覆盖 GORM 默认表名。
func (Payment) TableName() string { return "payments" }

// 以下方法满足 payment.Apply 需要的实体接口（状态 + 乐观锁版本）。
func (p *Payment) PaymentState() payment.State { return payment.State(p.State) }

// SetPaymentState 写回状态列，供 payment.Apply 的 CAS 更新使用。
func (p *Payment) SetPaymentState(s payment.State) { p.State = string(s) }

// GetVersion 返回乐观锁版本。
func (p *Payment) GetVersion() int64 { return p.Version }

// SetVersion 写回乐观锁版本。
func (p *Payment) SetVersion(v int64) { p.Version = v }
