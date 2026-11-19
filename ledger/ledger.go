// Package ledger 提供最小复式记账原语：事务 + 分录 + 余额。
//
// 约定：
//   - Posting.Amount 是有符号最小货币单位；
//   - 同一 Transaction 内按币种求和必须为 0；
//   - 分录只追加，不 update/delete；
//   - 余额由分录聚合得出（或由业务缓存，但必须可重算）。
package ledger

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/money"
	"gorm.io/gorm"
)

// Ledger 是复式记账入口。
type Ledger struct {
	// db 是账本读写使用的 gormx 客户端；未配置时各方法返回 Internal 错误。
	db *gormx.Client
}

// New 用给定的 gormx 客户端构造 Ledger。
func New(db *gormx.Client) *Ledger { return &Ledger{db: db} }

// Migrate 创建/补齐 ledger 表。生产环境应使用 migrate print 的 DDL。
func (l *Ledger) Migrate(ctx context.Context) error {
	if l == nil || l.db == nil || !l.db.Enabled() {
		return fxerrors.Internal("ledger: requires a configured database")
	}
	return l.db.Conn(ctx).AutoMigrate(&Account{}, &Transaction{}, &Posting{})
}

// Post 在一个 DB 事务内写入 transaction 与 postings，并校验借贷平衡。
func (l *Ledger) Post(ctx context.Context, txn Transaction, postings []Posting) (err error) {
	start := time.Now()
	result := "ok"
	defer func() { recordPost(ctx, result, len(postings), time.Since(start)) }()

	if l == nil || l.db == nil || !l.db.Enabled() {
		result = "invalid"
		return fxerrors.Internal("ledger: requires a configured database")
	}
	txn.ID = strings.TrimSpace(txn.ID)
	if txn.ID == "" {
		result = "invalid"
		return fxerrors.BadRequest("ledger: transaction id is required")
	}
	if len(postings) < 2 {
		result = "invalid"
		return fxerrors.BadRequest("ledger: at least two postings are required")
	}

	sums := make(map[string]int64)
	for i := range postings {
		p := &postings[i]
		p.TxnID = txn.ID
		p.AccountID = strings.TrimSpace(p.AccountID)
		if p.AccountID == "" {
			result = "invalid"
			return fxerrors.BadRequest("ledger: posting account_id is required")
		}
		if p.Amount == 0 {
			result = "invalid"
			return fxerrors.BadRequest("ledger: posting amount must not be zero")
		}
		m, err := money.New(p.Amount, p.Currency)
		if err != nil {
			result = "invalid"
			return fxerrors.Wrap(err)
		}
		p.Currency = string(m.Currency)
		sums[string(m.Currency)] += p.Amount
	}
	for currency, sum := range sums {
		if sum != 0 {
			result = "unbalanced"
			return fxerrors.BadRequest("ledger: postings for %s must sum to zero, got %d", currency, sum)
		}
	}

	_, err = l.db.WithTxResult(ctx, func(txCtx context.Context) (struct{}, error) {
		if err := l.db.Conn(txCtx).Create(&txn).Error; err != nil {
			return struct{}{}, mapLedgerError(err)
		}
		if err := l.db.Conn(txCtx).Create(&postings).Error; err != nil {
			return struct{}{}, mapLedgerError(err)
		}
		return struct{}{}, nil
	})
	if err != nil {
		result = "error"
		if fxerrors.Wrap(err).Kind == fxerrors.KindConflict {
			result = "conflict"
		}
	}
	return err
}

// Balance 返回账户在指定币种下的余额（分录求和）。
func (l *Ledger) Balance(ctx context.Context, accountID, currency string) (money.Money, error) {
	if l == nil || l.db == nil || !l.db.Enabled() {
		return money.Money{}, fxerrors.Internal("ledger: requires a configured database")
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return money.Money{}, fxerrors.BadRequest("ledger: account_id is required")
	}
	m, err := money.New(0, currency)
	if err != nil {
		return money.Money{}, fxerrors.Wrap(err)
	}
	var sum int64
	if err := l.db.Conn(ctx).Model(&Posting{}).
		Where("account_id = ? AND currency = ?", accountID, string(m.Currency)).
		Select("COALESCE(SUM(amount), 0)").
		Scan(&sum).Error; err != nil {
		return money.Money{}, fxerrors.Wrap(err)
	}
	return money.New(sum, string(m.Currency))
}

// Entries 返回某个事务的全部分录。
func (l *Ledger) Entries(ctx context.Context, txnID string) ([]Posting, error) {
	if l == nil || l.db == nil || !l.db.Enabled() {
		return nil, fxerrors.Internal("ledger: requires a configured database")
	}
	var rows []Posting
	if err := l.db.Conn(ctx).Where("txn_id = ?", strings.TrimSpace(txnID)).Order("id").Find(&rows).Error; err != nil {
		return nil, fxerrors.Wrap(err)
	}
	return rows, nil
}

// RegisterSchemaContracts 登记 ledger 表结构，供 migrate check 使用。
func RegisterSchemaContracts() {
	gormx.RegisterSchemaContract("ledger_accounts", "id", "currency", "type", "created_at")
	gormx.RegisterSchemaContract("ledger_transactions", "id", "reference", "created_at")
	gormx.RegisterSchemaContract("ledger_postings", "id", "txn_id", "account_id", "amount", "currency", "created_at")
}

// Module 是 opt-in 的 ledger 模块。
var Module = di.Module("fxkit/ledger",
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*Ledger, error) {
			client, err := di.Invoke[*gormx.Client](i)
			if err != nil {
				return nil, err
			}
			if client == nil || !client.Enabled() {
				return nil, fxerrors.Internal("ledger: requires a configured database")
			}
			return New(client), nil
		})
		RegisterSchemaContracts()
		if lc, err := di.Invoke[*di.Lifecycle](i); err == nil && lc != nil {
			lc.Append(di.Hook{
				OnStart: func(ctx context.Context) error {
					client, err := di.Invoke[*gormx.Client](i)
					if err != nil || client == nil || !client.Enabled() || !client.AutoMigrate() {
						return nil
					}
					return New(client).Migrate(ctx)
				},
			})
		}
	},
)

// mapLedgerError 把唯一键冲突转为 Conflict，其余非 nil 错误包装为 fxerrors。
func mapLedgerError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fxerrors.Conflict("ledger: duplicate transaction")
	}
	return fxerrors.Wrap(err)
}
