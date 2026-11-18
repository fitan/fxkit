package payments

import (
	"context"
	"sync"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

// Provider 是支付渠道抽象。真实实现应使用渠道幂等键。
type Provider interface {
	Authorize(ctx context.Context, p entity.Payment, idempotencyKey string) (string, error)
	Capture(ctx context.Context, p entity.Payment, idempotencyKey string) error
	Refund(ctx context.Context, p entity.Payment, amount int64, idempotencyKey string) error
	Settlements(ctx context.Context) ([]SettlementRow, error)
}

// MockProvider 是本地演示用渠道，不访问网络。
type MockProvider struct {
	mu      sync.Mutex
	settled map[string]SettlementRow
}

// NewMockProvider 创建本地演示渠道，不访问网络。
func NewMockProvider() *MockProvider {
	return &MockProvider{settled: make(map[string]SettlementRow)}
}

// Authorize 返回稳定的 mock 渠道引用；真实渠道应使用幂等键。
func (m *MockProvider) Authorize(_ context.Context, p entity.Payment, _ string) (string, error) {
	return "mock_" + p.ID, nil
}

// Capture 把支付单登记为渠道侧已结算，供对账使用。
func (m *MockProvider) Capture(_ context.Context, p entity.Payment, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settled[p.ID] = SettlementRow{PaymentID: p.ID, Amount: p.Amount, Currency: p.Currency}
	return nil
}

// Refund 按金额扣减渠道结算；退完则移除。
func (m *MockProvider) Refund(_ context.Context, p entity.Payment, amount int64, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.settled[p.ID]
	if !ok {
		return nil
	}
	row.Amount -= amount
	if row.Amount <= 0 {
		delete(m.settled, p.ID)
		return nil
	}
	m.settled[p.ID] = row
	return nil
}

// Settlements 返回渠道侧当前结算行。
func (m *MockProvider) Settlements(context.Context) ([]SettlementRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SettlementRow, 0, len(m.settled))
	for _, row := range m.settled {
		out = append(out, row)
	}
	return out, nil
}
