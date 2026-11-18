package ledger

import "time"

// Account 是账本账户。Type 由业务定义（asset/liability/equity/revenue/expense）。
type Account struct {
	ID        string `gorm:"primaryKey;size:128"`
	Currency  string `gorm:"size:3;not null"`
	Type      string `gorm:"size:32"`
	CreatedAt time.Time
}

func (Account) TableName() string { return "ledger_accounts" }

// Transaction 是一次复式记账事务。
type Transaction struct {
	ID        string `gorm:"primaryKey;size:128"`
	Reference string `gorm:"size:128;index"`
	CreatedAt time.Time
}

func (Transaction) TableName() string { return "ledger_transactions" }

// Posting 是事务中的一条分录。Amount 为有符号最小货币单位；
// 同一事务内按币种求和必须为 0。
type Posting struct {
	ID        uint64 `gorm:"primaryKey"`
	TxnID     string `gorm:"size:128;index;not null"`
	AccountID string `gorm:"size:128;index;not null"`
	Amount    int64  `gorm:"not null"`
	Currency  string `gorm:"size:3;not null"`
	CreatedAt time.Time
}

func (Posting) TableName() string { return "ledger_postings" }
