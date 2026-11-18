package entity

import "time"

// Refund 是一次退款记录。支持对同一支付单多次部分退款。
// State 目前只有 succeeded（示例的渠道调用是同步的）。
type Refund struct {
	// ID 是退款单 ID（TypeID：ref_...）。
	ID string `gorm:"primaryKey;size:64" json:"-"`
	// PaymentID 是所属支付单 ID。
	PaymentID string `gorm:"size:64;not null;index" json:"-"`
	// Amount 是本次退款金额（最小货币单位）。
	Amount int64 `gorm:"not null" json:"-"`
	// Currency 是 ISO 4217 币种。
	Currency string `gorm:"size:3;not null" json:"-"`
	// State 是退款状态（示例为 succeeded）。
	State string `gorm:"size:32;not null;index" json:"-"`
	// ProviderRef 是渠道退款引用。
	ProviderRef string `gorm:"size:128" json:"-"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"-"`
	// UpdatedAt 是更新时间。
	UpdatedAt time.Time `json:"-"`
}

// TableName 覆盖 GORM 默认表名。
func (Refund) TableName() string { return "payment_refunds" }
