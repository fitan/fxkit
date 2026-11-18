package demo

import (
	"context"

	"github.com/fitan/fxkit/fxerrors"
	"github.com/fitan/fxkit/gormx"
)

// TxAfterCommit 演示 gormx.AfterCommit：副作用只在事务真正提交后执行，
// 回滚时不会运行（因此 Hooks 不递增）。
func (s *Service) TxAfterCommit(ctx context.Context, fail bool) (TxDemo, error) {
	if s.db == nil {
		return TxDemo{}, fxerrors.Unavailable("db unavailable")
	}
	_, err := s.db.WithTxResult(ctx, func(txCtx context.Context) (struct{}, error) {
		gormx.AfterCommit(txCtx, func(context.Context) {
			s.afterCommit.Add(1)
		})
		if fail {
			return struct{}{}, fxerrors.Internal("demo: forced rollback")
		}
		s.committed.Add(1)
		return struct{}{}, nil
	})
	if err != nil {
		return TxDemo{Committed: false, Hooks: s.afterCommit.Load()}, err
	}
	return TxDemo{Committed: true, Hooks: s.afterCommit.Load()}, nil
}
