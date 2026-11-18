// Package payment 提供支付状态机原语：合法 transition + CAS 更新。
//
// 约定：实体必须有 GORM 列 state（string）和 version（int64），并实现 Entity。
// 状态机的具体状态/transition 由业务定义；本包不绑定任何支付渠道。
package payment

import (
	"context"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
)

// State 是支付状态。
type State string

// Transition 是一条合法状态流转。
type Transition struct {
	From State
	To   State
}

// Machine 是状态机。
type Machine struct {
	allowed map[State]map[State]struct{}
}

// NewMachine 声明合法 transition；未列出的 transition 一律拒绝。
func NewMachine(transitions ...Transition) *Machine {
	m := &Machine{allowed: make(map[State]map[State]struct{})}
	for _, t := range transitions {
		if m.allowed[t.From] == nil {
			m.allowed[t.From] = make(map[State]struct{})
		}
		m.allowed[t.From][t.To] = struct{}{}
	}
	return m
}

// Can 报告 from -> to 是否合法。
func (m *Machine) Can(from, to State) bool {
	if m == nil {
		return false
	}
	_, ok := m.allowed[from][to]
	return ok
}

// Entity 是状态机需要的实体能力。
type Entity interface {
	gormx.Versioned
	PaymentState() State
	SetPaymentState(State)
}

// Apply 校验 transition 后用乐观锁更新 state。
//
// 冲突（version 不匹配）或非法 transition 返回 409。
func Apply(ctx context.Context, db *gormx.Client, m *Machine, e Entity, to State) error {
	if e == nil {
		return fxerrors.Internal("payment: nil entity")
	}
	from := e.PaymentState()
	if !m.Can(from, to) {
		return fxerrors.Conflict("payment: invalid transition %s -> %s", from, to)
	}
	e.SetPaymentState(to)
	if err := gormx.UpdateWithVersion(ctx, db, e, "state"); err != nil {
		e.SetPaymentState(from)
		return err
	}
	return nil
}
