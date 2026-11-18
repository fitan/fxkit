package gormx

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	return NewTestClient(db)
}

// TestClient_DisabledConnPanics 禁用客户端是「可选依赖」的探测点，不能用于查询。
func TestClient_DisabledConnPanics(t *testing.T) {
	var c *Client
	assert.False(t, c.Enabled())
	assert.Nil(t, c.Pool())
	assert.Panics(t, func() { c.Conn(context.Background()) })
}

// TestClient_EnabledFalseWhenDBNotConfigured 未配置 db.driver/db.dsn 时 New 返回 nil（可选依赖）。
func TestClient_EnabledFalseWhenDBNotConfigured(t *testing.T) {
	c, err := New(&Config{})
	require.NoError(t, err)
	require.Nil(t, c)
	assert.False(t, c.Enabled())
	assert.Panics(t, func() { c.Conn(context.Background()) })
}

// TestClient_ConnUsesTxFromContext ctx 携带事务时，Conn 必须复用该事务，
// 否则业务里的「嵌套事务」会变成两条独立连接，破坏原子性。
func TestClient_ConnUsesTxFromContext(t *testing.T) {
	c := newTestClient(t)

	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context) error {
		tx, ok := TxFromContext(ctx)
		require.True(t, ok, "transaction ctx must carry the tx handle")

		conn := c.Conn(ctx)
		require.NotNil(t, conn)
		assert.True(t, conn.Statement.ConnPool == tx.Statement.ConnPool,
			"Conn inside a tx must reuse the same connection")
		return nil
	}))
}

// TestClient_ConnOutsideTxUsesPool 无事务时 Conn 走根连接池。
func TestClient_ConnOutsideTxUsesPool(t *testing.T) {
	c := newTestClient(t)
	conn := c.Conn(context.Background())
	require.NotNil(t, conn)
	assert.NotNil(t, c.Pool())

	// 不在事务内：ctx 中没有 tx，Conn 应该用根池而不是某个 tx 句柄
	_, inTx := TxFromContext(context.Background())
	assert.False(t, inTx, "plain context carries no transaction")
}

// TestClient_TransactionNestedSharesTx 嵌套 Transaction 不应开新事务。
func TestClient_TransactionNestedSharesTx(t *testing.T) {
	c := newTestClient(t)

	var outerCalls, innerCalls int
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context) error {
		outerCalls++
		return c.Transaction(ctx, func(ctx2 context.Context) error {
			innerCalls++
			// 嵌套内仍是同一个 tx
			outerTx, _ := TxFromContext(ctx)
			innerTx, _ := TxFromContext(ctx2)
			assert.Same(t, outerTx, innerTx)
			return nil
		})
	}))
	assert.Equal(t, 1, outerCalls)
	assert.Equal(t, 1, innerCalls)
}

// TestClient_TransactionNilClient 空客户端必须返回错误而不是 panic。
func TestClient_TransactionNilClient(t *testing.T) {
	var c *Client
	err := c.Transaction(context.Background(), func(context.Context) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil client")
}

// TestAfterCommit_RunsAfterSuccessfulCommit 回调必须在事务提交后执行。
func TestAfterCommit_RunsAfterSuccessfulCommit(t *testing.T) {
	c := newTestClient(t)

	var ran bool
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context) error {
		AfterCommit(ctx, func(context.Context) { ran = true })
		assert.False(t, ran, "callback must not run before commit")
		return nil
	}))
	assert.True(t, ran, "callback must run after commit")
}

// TestAfterCommit_SkippedOnRollback 事务回滚时回调不能执行。
func TestAfterCommit_SkippedOnRollback(t *testing.T) {
	c := newTestClient(t)

	var ran bool
	err := c.Transaction(context.Background(), func(ctx context.Context) error {
		AfterCommit(ctx, func(context.Context) { ran = true })
		return errors.New("boom")
	})
	require.Error(t, err)
	assert.False(t, ran, "callback must not run when the transaction rolled back")
}

// TestAfterCommit_NoTxRunsImmediately 无事务时立即执行。
func TestAfterCommit_NoTxRunsImmediately(t *testing.T) {
	var ran bool
	AfterCommit(context.Background(), func(context.Context) { ran = true })
	assert.True(t, ran)
}

// TestAfterCommit_NestedSharesOuterHooks 嵌套事务共用外层 hook 列表，
// 全部在最外层 commit 之后按注册顺序执行。
func TestAfterCommit_NestedSharesOuterHooks(t *testing.T) {
	c := newTestClient(t)

	var order []string
	require.NoError(t, c.Transaction(context.Background(), func(ctx context.Context) error {
		AfterCommit(ctx, func(context.Context) { order = append(order, "outer") })
		return c.Transaction(ctx, func(ctx2 context.Context) error {
			AfterCommit(ctx2, func(context.Context) { order = append(order, "inner") })
			return nil
		})
	}))

	assert.Equal(t, []string{"outer", "inner"}, order,
		"nested hooks share the outer list and run after the outermost commit")
}

// TestAfterCommit_PanicIsContained 回调 panic 不应让提交后的流程崩溃。
func TestAfterCommit_PanicIsContained(t *testing.T) {
	c := newTestClient(t)

	assert.NotPanics(t, func() {
		_ = c.Transaction(context.Background(), func(ctx context.Context) error {
			AfterCommit(ctx, func(context.Context) { panic("callback exploded") })
			return nil
		})
	})
}

// TestWithTxResult 泛型版本返回结果。
func TestWithTxResult(t *testing.T) {
	c := newTestClient(t)

	got, err := c.WithTxResult(context.Background(), func(ctx context.Context) (string, error) {
		return "ok", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", got)

	_, err = c.WithTxResult(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("fail")
	})
	require.Error(t, err)
}

// TestConfig_SetDefaultsAndValidate 默认值与校验。
//
// 注意：Validate 要求 log_level 必须是枚举值之一，所以「零值 Config」本身
// 不合法 —— SetDefaults 是必需步骤，而不是可选的。
func TestConfig_SetDefaultsAndValidate(t *testing.T) {
	var c Config
	c.SetDefaults()

	assert.NotZero(t, c.MaxOpenConns)
	assert.NotZero(t, c.MaxIdleConns)
	assert.NotZero(t, c.ConnMaxLifetime)
	assert.Equal(t, "warn", c.LogLevel)
	assert.NoError(t, c.Validate(), "defaults must be valid")

	// 零值 Config：log_level 为空 → 不合法（证明 SetDefaults 必需）
	var zero Config
	assert.Error(t, zero.Validate(), "zero Config must be rejected; SetDefaults is required")

	// driver/dsn 必须成对出现
	onlyDriver := Config{}
	onlyDriver.SetDefaults()
	onlyDriver.Driver = "sqlite"
	assert.Error(t, onlyDriver.Validate(), "driver without dsn must be rejected")

	// 未知 driver 必须拒绝
	bad := Config{}
	bad.SetDefaults()
	bad.Driver = "oracle"
	bad.DSN = "x"
	assert.Error(t, bad.Validate(), "unknown driver must be rejected")
}

// TestDBHealthChecker_NamesAndChecks 健康检查项的名字与行为。
func TestDBHealthChecker_NamesAndChecks(t *testing.T) {
	c := newTestClient(t)
	hc := dbHealthChecker{client: c}

	assert.Equal(t, "db", hc.Name())
	assert.NoError(t, hc.Check(context.Background()), "healthy sqlite pool must pass")

	// nil client 必须报错而不是 panic
	assert.Error(t, dbHealthChecker{}.Check(context.Background()))
}
