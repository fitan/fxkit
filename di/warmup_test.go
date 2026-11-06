package di

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type warmDep struct{ V string }

type warmSvc struct{ D *warmDep }

// TestWarmup_CatchesConstructorError 是核心场景：
// 构造函数返回 error 时，lazy 注册的服务在启动期完全不可见，
// 要等第一次 Invoke 才暴露。Warmup 把它提前到启动。
func TestWarmup_CatchesConstructorError(t *testing.T) {
	root := New()
	defer Shutdown(root)

	boom := errors.New("db connection failed")
	Provide(root, func(Injector) (*warmSvc, error) { return nil, boom })

	report := Warmup(root)

	assert.False(t, report.OK(), "constructor error must be reported")
	require.Len(t, report.Failed, 1)
	assert.Contains(t, report.Failed[0].Service, "warmSvc")
	assert.ErrorIs(t, report.Failed[0].Err, boom)

	require.Error(t, report.Error())
	assert.Contains(t, report.Error().Error(), "db connection failed")
}

// TestWarmup_MissingDepIsOptional 「依赖缺失」归入 Optional：
// 未配置 Redis 时没有 cache 服务是合法的，不应让启动失败。
func TestWarmup_MissingDepIsOptional(t *testing.T) {
	root := New()
	defer Shutdown(root)

	// 依赖一个不存在的服务
	provideWarmSvc(root)

	report := Warmup(root)

	assert.True(t, report.OK(), "missing dependency should not be fatal")
	assert.Empty(t, report.Failed)
	require.Len(t, report.Optional, 1)
	assert.Contains(t, report.Optional[0].Service, "warmSvc")
}

// TestWarmup_HealthyGraphResolvesEverything 全部可构造时，所有服务都应被解析。
func TestWarmup_HealthyGraphResolvesEverything(t *testing.T) {
	root := New(func(i Injector) { ProvideValue(i, &warmDep{V: "x"}) })
	defer Shutdown(root)

	provideWarmSvc(root)

	report := Warmup(root)

	require.True(t, report.OK(), "report: %+v", report)
	assert.Empty(t, report.Failed)
	assert.Empty(t, report.Optional)

	// 所有已注册服务（含 Lifecycle）都应被解析
	services, _ := Describe(root)
	assert.Len(t, report.Resolved, len(services),
		"every registered service should be resolved; resolved=%v services=%d", report.Resolved, len(services))
}

// TestWarmup_NilInjector nil 容器不应 panic。
func TestWarmup_NilInjector(t *testing.T) {
	assert.NotPanics(t, func() {
		report := Warmup(nil)
		assert.True(t, report.OK())
	})
}

// TestAssertNoUnresolved_StrictOnOptional 严格版把 Optional 也算错误，
// 适合在测试里断言「整张图完全可构造」。
func TestAssertNoUnresolved_StrictOnOptional(t *testing.T) {
	root := New()
	defer Shutdown(root)
	provideWarmSvc(root)

	err := AssertNoUnresolved(root)
	require.Error(t, err, "strict mode must flag unresolvable dependencies")
	assert.True(t,
		strings.Contains(err.Error(), "unresolvable dependencies") ||
			strings.Contains(err.Error(), "could not find service"),
		"got: %v", err)
}

// TestAssertNoUnresolved_Healthy 健康图应通过严格检查。
func TestAssertNoUnresolved_Healthy(t *testing.T) {
	root := New(func(i Injector) { ProvideValue(i, &warmDep{V: "x"}) })
	defer Shutdown(root)
	provideWarmSvc(root)

	assert.NoError(t, AssertNoUnresolved(root))
}

// TestWarmup_Idempotent 重复预热不应重复构造（do 内部缓存）。
func TestWarmup_Idempotent(t *testing.T) {
	root := New(func(i Injector) { ProvideValue(i, &warmDep{V: "x"}) })
	defer Shutdown(root)

	var builds int
	ProvideNamed[any](root, "counter-probe", func(Injector) (any, error) {
		builds++
		return "built", nil
	})

	Warmup(root)
	Warmup(root)

	assert.Equal(t, 1, builds, "service must be constructed once and cached")
}

// provideWarmSvc 注册一个依赖 *warmDep 的服务。
// 用 Invoke 取依赖（而不是 MustInvoke），这样「依赖缺失」会作为构造错误上报，
// 便于测试 Warmup 的 Optional 分类。
func provideWarmSvc(root Injector) {
	Provide(root, func(i Injector) (*warmSvc, error) {
		d, err := Invoke[*warmDep](i)
		if err != nil {
			return nil, err
		}
		return &warmSvc{D: d}, nil
	})
}
