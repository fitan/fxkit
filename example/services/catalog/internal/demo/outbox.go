package demo

import (
	"context"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/outbox"
)

// OutboxFailed 列出进入 DLQ（status=failed）的事件，供排查与重放。
func (s *Service) OutboxFailed(ctx context.Context, limit int) ([]OutboxFailed, error) {
	if !s.outboxEnabled() {
		return nil, fxerrors.Unavailable("outbox disabled")
	}
	rows, err := s.store.ListFailed(ctx, limit)
	if err != nil {
		return nil, fxerrors.Wrap(err)
	}
	out := make([]OutboxFailed, 0, len(rows))
	for _, r := range rows {
		out = append(out, OutboxFailed{
			ID:        r.ID,
			Topic:     r.Topic,
			Attempts:  r.Attempts,
			LastError: r.LastError,
			CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// RequeueOutbox 把 failed 行重置为 pending，交给 relay 重投（at-least-once）。
func (s *Service) RequeueOutbox(ctx context.Context, req RequeueOutboxReq) (RequeueOutboxResult, error) {
	if !s.outboxEnabled() {
		return RequeueOutboxResult{}, fxerrors.Unavailable("outbox disabled")
	}
	n, err := s.store.RequeueFailed(ctx, outbox.RequeueFilter{Topics: req.Topics, Limit: req.Limit})
	if err != nil {
		return RequeueOutboxResult{}, fxerrors.Wrap(err)
	}
	return RequeueOutboxResult{Requeued: n}, nil
}
