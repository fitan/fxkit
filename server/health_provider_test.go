package server_test

import (
	"context"
	"testing"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// namedChecker 是测试用的具名实现。
type namedChecker struct{ name string }

func (c namedChecker) Name() string                { return c.name }
func (c namedChecker) Check(context.Context) error { return nil }

// TestProvideHealthChecker_AllFormsShareOneGroup 是回归测试：
// gormx / cachex 曾各自声明「结构相同的匿名接口」并注册到 group，
// 但 samber/do 按类型名字符串区分服务，于是 group 被拆成多个条目，
// /readyz 只能看到其中一个（实测缺失 "db"）。
//
// 现在统一用 server.HealthChecker 这个具名类型，本测试确保所有注册形式
// 都落进**同一个** group。
func TestProvideHealthChecker_AllFormsShareOneGroup(t *testing.T) {
	root := di.New(
		// 直接给值
		func(i di.Injector) {
			server.ProvideHealthChecker(namedChecker{name: "direct"})(i)
		},
		// 延迟构造（可访问依赖图）
		func(i di.Injector) {
			server.ProvideHealthCheckerLazy(func(di.Injector) (server.HealthChecker, error) {
				return namedChecker{name: "from-injector"}, nil
			})(i)
		},
	)
	defer root.Shutdown()

	// 触发 PreStart 以解析延迟构造函数
	lc := di.MustInvoke[*di.Lifecycle](root)
	require.NoError(t, lc.StartPreStartOnly(context.Background()))

	checkers := di.GetGroup[server.HealthChecker](root)
	names := make([]string, 0, len(checkers))
	for _, c := range checkers {
		names = append(names, c.Name())
	}
	assert.ElementsMatch(t, []string{"direct", "from-injector"}, names,
		"all registration forms must land in the same group")
}

// TestProvideHealthChecker_ConstructorForm 验证 func(dep...) 形式延迟装配。
func TestProvideHealthChecker_ConstructorForm(t *testing.T) {
	type dep struct{ v string }

	root := di.New(
		func(i di.Injector) { di.ProvideValue(i, &dep{v: "injected"}) },
		func(i di.Injector) {
			server.ProvideHealthCheckerLazy(func(i di.Injector) (server.HealthChecker, error) {
				return namedChecker{name: di.MustInvoke[*dep](i).v}, nil
			})(i)
		},
	)
	defer root.Shutdown()

	require.NoError(t, di.MustInvoke[*di.Lifecycle](root).StartPreStartOnly(context.Background()))

	checkers := di.GetGroup[server.HealthChecker](root)
	require.Len(t, checkers, 1)
	assert.Equal(t, "injected", checkers[0].Name())
}
