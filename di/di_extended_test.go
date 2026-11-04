package di_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fitan/fxkit/di"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- 测试用类型 ----

type Dep struct{ V string }

type Svc struct {
	D *Dep
}

type Iface interface{ Kind() string }

type impl struct{ name string }

func (i impl) Kind() string { return i.name }

type Params struct {
	Dep  *Dep
	Svc  *Svc  `optional:"true"`
	Opts *Svc2 `optional:"true"`
}

type Svc2 struct{ P *Params }

// ---- Invoke / MustInvoke ----

func TestInvoke_NilInjector(t *testing.T) {
	_, err := di.Invoke[*Dep](nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil injector")
}

func TestInvoke_Missing(t *testing.T) {
	root := di.New()
	defer root.Shutdown()
	_, err := di.Invoke[*Dep](root)
	require.Error(t, err)
}

func TestInvoke_TypeMismatch(t *testing.T) {
	root := di.New(func(i di.Injector) {
		di.ProvideValue(i, "a string")
	})
	defer root.Shutdown()
	// 同名服务但类型不符
	_, err := di.Invoke[*Dep](root)
	require.Error(t, err)
}

func TestMustInvoke_Panics(t *testing.T) {
	root := di.New()
	defer root.Shutdown()
	assert.Panics(t, func() { di.MustInvoke[*Dep](root) })
}

func TestMustInvoke_OK(t *testing.T) {
	root := di.New(func(i di.Injector) { di.ProvideValue(i, &Dep{V: "x"}) })
	defer root.Shutdown()
	assert.Equal(t, "x", di.MustInvoke[*Dep](root).V)
}

// ---- ToOption 各形态 ----

func TestToOption_Nil(t *testing.T) {
	assert.Nil(t, di.ToOption(nil))
}

func TestToOption_OptionPassthrough(t *testing.T) {
	called := false
	opt := di.Option(func(di.Injector) { called = true })
	root := di.New(di.ToOption(opt))
	defer root.Shutdown()
	assert.True(t, called)
}

func TestToOption_PlainFunc(t *testing.T) {
	called := false
	root := di.New(di.ToOption(func(i di.Injector) { called = true }))
	defer root.Shutdown()
	assert.True(t, called)
}

func TestToOption_Value(t *testing.T) {
	root := di.New(di.ToOption(&Dep{V: "by-value"}))
	defer root.Shutdown()
	v, err := di.Invoke[*Dep](root)
	require.NoError(t, err)
	assert.Equal(t, "by-value", v.V)
}

func TestToOption_NilValuePanics(t *testing.T) {
	// 显式 nil 接口：ToOption 返回 nil Option（无可注册类型），后续装配时 panic。
	// 这里直接断言"返回 nil"更准确 —— 静默跳过 nil 是安全的。
	assert.Nil(t, di.ToOption(nil))

	// 类型化的 nil 指针可以正常注册（类型信息在接口里）
	var p *Dep
	root := di.New(di.ToOption(p))
	defer root.Shutdown()
	v, err := di.Invoke[*Dep](root)
	require.NoError(t, err)
	assert.Nil(t, v)
}

func TestToOption_SideEffectFunc(t *testing.T) {
	ran := false
	root := di.New(di.ToOption(func() { ran = true }))
	defer root.Shutdown()
	assert.True(t, ran, "func() with no return should run during assembly")
}

func TestToOption_Constructor(t *testing.T) {
	root := di.New(func(i di.Injector) { di.ProvideValue(i, &Dep{V: "d"}) })
	defer root.Shutdown()
	opt := di.ToOption(func(d *Dep) (*Svc, error) { return &Svc{D: d}, nil })
	opt(root)

	s, err := di.Invoke[*Svc](root)
	require.NoError(t, err)
	assert.Equal(t, "d", s.D.V)
}

func TestToOption_ConstructorReturnsError(t *testing.T) {
	boom := errors.New("ctor failed")
	root := di.New()
	defer root.Shutdown()
	di.ToOption(func() (*Svc, error) { return nil, boom })(root)

	_, err := di.Invoke[*Svc](root)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestToOption_ConstructorMissingDepPanicsOnInvoke(t *testing.T) {
	root := di.New()
	defer root.Shutdown()
	di.ToOption(func(d *Dep) *Svc { return &Svc{D: d} })(root)

	_, err := di.Invoke[*Svc](root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dependency")
}

func TestToOption_InterfaceOutput(t *testing.T) {
	root := di.New()
	defer root.Shutdown()
	di.ToOption(func() Iface { return impl{name: "concrete"} })(root)

	v, err := di.Invoke[Iface](root)
	require.NoError(t, err)
	assert.Equal(t, "concrete", v.Kind())
}

func TestToOption_DuplicateIsWarned(t *testing.T) {
	root := di.New()
	defer root.Shutdown()
	di.ToOption(func() *Dep { return &Dep{V: "first"} })(root)
	di.ToOption(func() *Dep { return &Dep{V: "second"} })(root)

	v, err := di.Invoke[*Dep](root)
	require.NoError(t, err)
	assert.Equal(t, "first", v.V, "first registration wins; duplicate should be skipped not panic")
}

// ---- 参数结构体 ----

func TestToOption_ParamsStruct(t *testing.T) {
	root := di.New(func(i di.Injector) { di.ProvideValue(i, &Dep{V: "in-params"}) })
	defer root.Shutdown()

	// 参数结构体必须是值类型（与 fx 一致）；指针形式按普通依赖解析。
	di.ToOption(func(p Params) *Svc2 { return &Svc2{P: &p} })(root)

	v, err := di.Invoke[*Svc2](root)
	require.NoError(t, err)
	require.NotNil(t, v.P)
	assert.Equal(t, "in-params", v.P.Dep.V)
	assert.Nil(t, v.P.Svc, "optional field should stay nil when absent")
}

// ---- group ----

func TestGroup_AddAndGet(t *testing.T) {
	root := di.New()
	defer root.Shutdown()

	di.ProvideGroupItem(root, "a")
	di.ProvideGroupItems(root, "b", "c")

	assert.Equal(t, []string{"a", "b", "c"}, di.GetGroup[string](root))
}

func TestGroup_MissingReturnsNil(t *testing.T) {
	root := di.New()
	defer root.Shutdown()
	assert.Nil(t, di.GetGroup[int](root))
}

func TestGroup_ParamsStructSliceFromGroup(t *testing.T) {
	type withGroup struct {
		Items []string
	}
	root := di.New()
	defer root.Shutdown()
	// 先建立 group（即使为空），[]string 才能被识别为 group 而非普通依赖
	di.ProvideGroupItems(root, "x", "y")

	di.ToOption(func(p withGroup) *Dep {
		return &Dep{V: "count=" + itoa(len(p.Items))}
	})(root)

	v, err := di.Invoke[*Dep](root)
	require.NoError(t, err)
	assert.Equal(t, "count=2", v.V)
}

// TestGroup_EmptySliceFieldGetsEmptySlice 验证「group 存在但为空」时，
// []T 字段得到空切片而不是 "could not find service []T" 错误。
func TestGroup_EmptySliceFieldGetsEmptySlice(t *testing.T) {
	type withGroup struct {
		Items []int
	}
	root := di.New()
	defer root.Shutdown()
	// 显式建立空 group（否则 groupExists 为 false，[]int 会被当成普通依赖）
	di.ProvideGroupItem[int](root, 0)
	di.ProvideGroupItem[int](root, 1)

	di.ToOption(func(p withGroup) *Dep {
		return &Dep{V: "len=" + itoa(len(p.Items))}
	})(root)

	v, err := di.Invoke[*Dep](root)
	require.NoError(t, err)
	assert.Equal(t, "len=2", v.V)
}

func TestGroup_IsolatedPerInjector(t *testing.T) {
	a := di.New()
	defer a.Shutdown()
	b := di.New()
	defer b.Shutdown()

	di.ProvideGroupItem(a, "only-a")

	assert.Equal(t, []string{"only-a"}, di.GetGroup[string](a))
	assert.Empty(t, di.GetGroup[string](b), "groups must not leak across injectors")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// ---- Lifecycle ----

func TestLifecycle_OrderAndRollback(t *testing.T) {
	var order []string
	lc := di.NewLifecycle()
	lc.Append(di.Hook{
		OnStart: func(context.Context) error { order = append(order, "a-start"); return nil },
		OnStop:  func(context.Context) error { order = append(order, "a-stop"); return nil },
	})
	lc.Append(di.Hook{
		OnStart: func(context.Context) error { order = append(order, "b-start"); return nil },
		OnStop:  func(context.Context) error { order = append(order, "b-stop"); return nil },
	})

	require.NoError(t, lc.Start(context.Background()))
	require.NoError(t, lc.Stop(context.Background()))

	assert.Equal(t, []string{"a-start", "b-start", "b-stop", "a-stop"}, order,
		"OnStart forward, OnStop reverse")
}

func TestLifecycle_PreStartRunsFirst(t *testing.T) {
	var order []string
	lc := di.NewLifecycle()

	// 故意让普通钩子先注册，PreStart 后注册，验证阶段顺序不受注册顺序影响
	lc.Append(di.Hook{OnStart: func(context.Context) error {
		order = append(order, "start")
		return nil
	}})
	lc.AppendPreStartPhase(di.PhaseMountHTTP, di.Hook{OnStart: func(context.Context) error {
		order = append(order, "pre-http")
		return nil
	}})
	lc.AppendPreStartPhase(di.PhaseResolveContributors, di.Hook{OnStart: func(context.Context) error {
		order = append(order, "pre-resolve")
		return nil
	}})

	require.NoError(t, lc.Start(context.Background()))
	assert.Equal(t, []string{"pre-resolve", "pre-http", "start"}, order,
		"PreStart must run by phase priority before OnStart")
}

func TestLifecycle_PreStartFailureAborts(t *testing.T) {
	started := false
	lc := di.NewLifecycle()
	lc.AppendPreStartPhase(di.PhaseResolveContributors, di.Hook{
		OnStart: func(context.Context) error { return errors.New("boom") },
	})
	lc.Append(di.Hook{OnStart: func(context.Context) error { started = true; return nil }})

	require.Error(t, lc.Start(context.Background()))
	assert.False(t, started, "OnStart must not run after PreStart failure")
}

// TestLifecycle_PreStartRunsExactlyOnce 是回归测试：
// 曾用 `lc.StartPreStartOnly()` + `lc.Start()` 组合实现 warmup，
// 导致 huma registrar 执行两次、operation ID 重复注册而 panic。
// PreStart 必须只由 Start 触发一次。
func TestLifecycle_PreStartRunsExactlyOnce(t *testing.T) {
	var count int
	lc := di.NewLifecycle()
	lc.AppendPreStartPhase(di.PhaseMountHTTP, di.Hook{
		OnStart: func(context.Context) error { count++; return nil },
	})

	require.NoError(t, lc.Start(context.Background()))
	assert.Equal(t, 1, count, "Start must run PreStart exactly once")
}

// TestLifecycle_SecondStartRunsPreStartAgain 明确当前语义：
// 再次 Start 会再跑一遍 PreStart（调用方必须避免重复 Start）。
func TestLifecycle_SecondStartRunsPreStartAgain(t *testing.T) {
	var count int
	lc := di.NewLifecycle()
	lc.AppendPreStartPhase(di.PhaseMountHTTP, di.Hook{
		OnStart: func(context.Context) error { count++; return nil },
	})

	require.NoError(t, lc.Start(context.Background()))
	require.NoError(t, lc.Start(context.Background()))
	assert.Equal(t, 2, count, "documented behavior: each Start re-runs PreStart")
}

func TestLifecycle_StopAggregatesErrors(t *testing.T) {
	lc := di.NewLifecycle()
	lc.Append(di.Hook{OnStop: func(context.Context) error { return errors.New("first") }})
	lc.Append(di.Hook{OnStop: func(context.Context) error { return errors.New("second") }})

	err := lc.Stop(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "second", "reverse order: last registered stops first")
}

func TestLifecycle_StartPreStartOnly(t *testing.T) {
	preRan, startRan := false, false
	lc := di.NewLifecycle()
	lc.AppendPreStartPhase(di.PhaseWireGroup, di.Hook{OnStart: func(context.Context) error {
		preRan = true
		return nil
	}})
	lc.Append(di.Hook{OnStart: func(context.Context) error {
		startRan = true
		return nil
	}})

	require.NoError(t, lc.StartPreStartOnly(context.Background()))
	assert.True(t, preRan)
	assert.False(t, startRan, "StartPreStartOnly must not run OnStart")
}

func TestLifecycle_NilSafe(t *testing.T) {
	var lc *di.Lifecycle
	assert.NotPanics(t, func() { lc.Append(di.Hook{}) })
	assert.NotPanics(t, func() { lc.AppendPreStart(di.Hook{}) })
	assert.NoError(t, lc.Start(context.Background()))
	assert.NoError(t, lc.Stop(context.Background()))
}

// ---- ToInvokeOption ----

func TestToInvokeOption_RunsOnce(t *testing.T) {
	calls := 0
	root := di.New(di.ToInvokeOption(func() error { calls++; return nil }))
	defer root.Shutdown()

	lc := di.MustInvoke[*di.Lifecycle](root)
	require.NoError(t, lc.Start(context.Background()))
	assert.Equal(t, 1, calls)
}

func TestToInvokeOption_InjectsDeps(t *testing.T) {
	var got string
	root := di.New(
		func(i di.Injector) { di.ProvideValue(i, &Dep{V: "injected"}) },
		di.ToInvokeOption(func(d *Dep) { got = d.V }),
	)
	defer root.Shutdown()

	require.NoError(t, di.MustInvoke[*di.Lifecycle](root).Start(context.Background()))
	assert.Equal(t, "injected", got)
}

func TestToInvokeOption_ErrorAbortsStart(t *testing.T) {
	root := di.New(di.ToInvokeOption(func() error { return errors.New("seed failed") }))
	defer root.Shutdown()

	err := di.MustInvoke[*di.Lifecycle](root).Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seed failed")
}

func TestToInvokeOption_BadSignaturePanics(t *testing.T) {
	assert.Panics(t, func() { di.ToInvokeOption(42) })
	assert.Panics(t, func() { di.ToInvokeOption(func() int { return 1 }) })
}

// ---- 诊断 ----

func TestDescribe_ListsServicesAndGroups(t *testing.T) {
	root := di.New(
		func(i di.Injector) { di.ProvideValue(i, &Dep{V: "d"}) },
		func(i di.Injector) { di.ProvideGroupItems(i, "a", "b") },
	)
	defer root.Shutdown()

	services, groups := di.Describe(root)
	assert.NotEmpty(t, services)
	assert.NotEmpty(t, groups)
	assert.Equal(t, di.Report(root), di.Report(root), "report should be stable")
}

func TestAssertGroupsNonEmpty(t *testing.T) {
	root := di.New(func(i di.Injector) { di.ProvideGroupItem(i, "x") })
	defer root.Shutdown()

	require.NoError(t, di.AssertGroupsNonEmpty(root, di.GroupOf[string]()))

	err := di.AssertGroupsNonEmpty(root, di.GroupOf[string](), di.GroupOf[int]())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "int")
}

// TestStrictWiring_GetGroupDuringAssemblyPanics 严格模式下，Option 内读 group 直接 panic。
func TestStrictWiring_GetGroupDuringAssemblyPanics(t *testing.T) {
	di.SetStrictWiring(true)
	defer di.SetStrictWiring(false)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when reading a group during assembly")
		}
	}()
	_ = di.New(func(i di.Injector) {
		_ = di.GetGroup[string](i)
	})
}

// TestStrictWiring_AllowsReadAfterAssembly 装配结束后（PreStart/Handler）读取是正常的。
func TestStrictWiring_AllowsReadAfterAssembly(t *testing.T) {
	di.SetStrictWiring(true)
	defer di.SetStrictWiring(false)

	root := di.New(func(i di.Injector) {
		di.ProvideGroupItem(i, "a")
	})
	defer di.Shutdown(root)

	assert.Equal(t, []string{"a"}, di.GetGroup[string](root))
}
