package demo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAfterCommitRunsOnlyOnCommit 验证提交才执行 hook、回滚不执行。
func TestAfterCommitRunsOnlyOnCommit(t *testing.T) {
	svc, _ := newDemoService(t, demoDeps{})
	ctx := context.Background()

	ok, err := svc.TxAfterCommit(ctx, false)
	require.NoError(t, err)
	require.True(t, ok.Committed)
	require.Equal(t, int64(1), ok.Hooks)

	// 回滚：AfterCommit 不应执行。
	_, err = svc.TxAfterCommit(ctx, true)
	require.Error(t, err)

	ok, err = svc.TxAfterCommit(ctx, false)
	require.NoError(t, err)
	require.True(t, ok.Committed)
	require.Equal(t, int64(2), ok.Hooks)
}
