package ledger

import "time"

// Account 是账本账户。Type 由业务定义（asset/liability/equity/revenue/expense）。
type Account struct {
	// ID 是账户 id，主键，最长 128 字符。
	ID string `gorm:"primaryKey;size:128"`
	// Currency 是账户币种（ISO 4217 三字母），最长 3 字符。
	Currency string `gorm:"size:3;not null"`
	// Type 是业务定义的账户类型，最长 32 字符。
	Type string `gorm:"size:32"`
	// CreatedAt 是创建时间，由 GORM 在插入时自动填充。
	CreatedAt time.Time
}

// TableName 返回表名 ledger_accounts。
func (Account) TableName() string { return "ledger_accounts" }

// Transaction 是一次复式记账事务。
type Transaction struct {
	// ID 是事务 id，主键，最长 128 字符；[Ledger.Post] 要求非空。
	ID string `gorm:"primaryKey;size:128"`
	// Reference 是外部引用号（如业务单号），最长 128 字符，带索引。
	Reference string `gorm:"size:128;index"`
	// CreatedAt 是创建时间，由 GORM 在插入时自动填充。
	CreatedAt time.Time
}

// TableName 返回表名 ledger_transactions。
func (Transaction) TableName() string { return "ledger_transactions" }

// Posting 是事务中的一条分录。Amount 为有符号最小货币单位；
// 同一事务内按币种求和必须为 0。
type Posting struct {
	// ID 是自增主键。
	ID uint64 `gorm:"primaryKey"`
	// TxnID 是所属事务 id，带索引；[Ledger.Post] 会用事务 id 覆写它。
	TxnID string `gorm:"size:128;index;not null"`
	// AccountID 是账户 id，带索引；[Ledger.Post] 要求非空。
	AccountID string `gorm:"size:128;index;not null"`
	// Amount 是有符号金额（最小货币单位）；[Ledger.Post] 要求不为 0。
	Amount int64 `gorm:"not null"`
	// Currency 是币种（ISO 4217 三字母）；[Ledger.Post] 会规范化为大写。
	Currency string `gorm:"size:3;not null"`
	// CreatedAt 是创建时间，由 GORM 在插入时自动填充。
	CreatedAt time.Time
}

// TableName 返回表名 ledger_postings。
func (Posting) TableName() string { return "ledger_postings" }
