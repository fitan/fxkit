// Package money 提供最小货币单位（minor units）的金额类型。
//
// 约定：Amount 是整数最小单位（如分），不使用 float；Currency 是 ISO 4217 三字母代码。
package money

import (
	"fmt"
	"strings"
)

// Currency 是 ISO 4217 货币代码（大写三字母）。
type Currency string

// Money 是某个币种的金额。
type Money struct {
	Amount   int64    `json:"amount"`
	Currency Currency `json:"currency"`
}

// New 校验币种并构造 Money。
func New(amount int64, currency string) (Money, error) {
	c := Currency(strings.ToUpper(strings.TrimSpace(currency)))
	if !validCurrency(c) {
		return Money{}, fmt.Errorf("money: invalid currency %q", currency)
	}
	return Money{Amount: amount, Currency: c}, nil
}

// MustNew 与 New 相同，非法币种直接 panic（仅用于常量/测试）。
func MustNew(amount int64, currency string) Money {
	m, err := New(amount, currency)
	if err != nil {
		panic(err)
	}
	return m
}

// Add 返回同币种相加结果。
func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("money: currency mismatch %s != %s", m.Currency, other.Currency)
	}
	return Money{Amount: m.Amount + other.Amount, Currency: m.Currency}, nil
}

// Sub 返回同币种相减结果。
func (m Money) Sub(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("money: currency mismatch %s != %s", m.Currency, other.Currency)
	}
	return Money{Amount: m.Amount - other.Amount, Currency: m.Currency}, nil
}

// Neg 返回相反数。
func (m Money) Neg() Money {
	return Money{Amount: -m.Amount, Currency: m.Currency}
}

// Mul 返回乘以整数倍的结果。
func (m Money) Mul(n int64) Money {
	return Money{Amount: m.Amount * n, Currency: m.Currency}
}

// Compare 返回 -1 / 0 / 1；币种不同返回错误。
func (m Money) Compare(other Money) (int, error) {
	if m.Currency != other.Currency {
		return 0, fmt.Errorf("money: currency mismatch %s != %s", m.Currency, other.Currency)
	}
	switch {
	case m.Amount < other.Amount:
		return -1, nil
	case m.Amount > other.Amount:
		return 1, nil
	default:
		return 0, nil
	}
}

// IsZero 报告金额是否为 0。
func (m Money) IsZero() bool { return m.Amount == 0 }

func (m Money) String() string {
	return fmt.Sprintf("%d %s", m.Amount, m.Currency)
}

func validCurrency(c Currency) bool {
	if len(c) != 3 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
