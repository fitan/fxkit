package di_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fitan/fxkit/di"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- 测试用类型 ----

type Dep struct{ V string }

type Svc struct{ D *Dep }

type Iface interface{ Kind() string }

type impl struct{ name string }

func (i impl) Kind() string { return i.name }

// ---- Invoke / MustInvoke ----

func TestInvoke_NilInjector(t *testing.T) {
	_, err := di.Invoke[*Dep](nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil injector")
}

func TestInvoke_Missing(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)

	_, err := di.Invoke[*Dep](root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not find service")
}

func TestMustInvoke_Panics(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)

	assert.Panics(t, func() { di.MustInvoke[*Dep](root) })
}

func TestMustInvoke_OK(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)
	di.ProvideValue(root, &Dep{V: "ok"})

	assert.Equal(t, "ok", di.MustInvoke[*Dep](root).V)
}

// ---- Provide ----

func TestProvide_Constructor(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)
	di.ProvideValue(root, &Dep{V: "in-ctor"})
	di.Provide(root, func(i di.Injector) (*Svc, error) {
		return &Svc{D: di.MustInvoke[*Dep](i)}, nil
	})

	v, err := di.Invoke[*Svc](root)
	require.NoError(t, err)
	assert.Equal(t, "in-ctor", v.D.V)
}

func TestProvide_ConstructorReturnsError(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)
	boom := errors.New("boom")
	di.Provide(root, func(di.Injector) (*Svc, error) { return nil, boom })

	_, err := di.Invoke[*Svc](root)
	require.ErrorIs(t, err, boom)
}

// 构造函数里依赖缺失时，错误必须是「找不到服务」而不是静默零值。
func TestProvide_MissingDepSurfaces(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)
	di.Provide(root, func(i di.Injector) (*Svc, error) {
		d, err := di.Invoke[*Dep](i)
		if err != nil {
			return nil, err
		}
		return &Svc{D: d}, nil
	})

	_, err := di.Invoke[*Svc](root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not find service")
}

func TestProvide_InterfaceOutput(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)
	di.Provide(root, func(di.Injector) (Iface, error) { return impl{name: "concrete"}, nil })

	v, err := di.Invoke[Iface](root)
	require.NoError(t, err)
	assert.Equal(t, "concrete", v.Kind())
}

// 重复注册必须装配期失败：早期实现只告警并静默取第一个。
func TestProvide_DuplicatePanics(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)
	di.ProvideValue(root, &Dep{V: "first"})

	assert.Panics(t, func() { di.ProvideValue(root, &Dep{V: "second"}) })
}

// ---- group ----

func TestGroup_AddAndGet(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)

	di.ProvideGroupItem(root, "a")
	di.ProvideGroupItems(root, "b", "c")

	assert.Equal(t, []string{"a", "b", "c"}, di.GetGroup[string](root))
}

func TestGroup_MissingReturnsNil(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)

	assert.Nil(t, di.GetGroup[string](root))
}

func TestGroup_IsolatedPerInjector(t *testing.T) {
	a := di.New()
	defer di.Shutdown(a)
	b := di.New()
	defer di.Shutdown(b)

	di.ProvideGroupItem(a, "only-a")
	assert.Empty(t, di.GetGroup[string](b))
}

// ---- Lifecycle ----

func TestLifecycle_OrderAndRollback(t *testing.T) {
	lc := di.NewLifecycle()
	var order []string
	lc.Append(di.Hook{
		OnStart: func(context.Context) error { order = append(order, "start1"); return nil },
		OnStop:  func(context.Context) error { order = append(order, "stop1"); return nil },
	})
	lc.Append(di.Hook{
		OnStart: func(context.Context) error { order = append(order, "start2"); return nil },
		OnStop:  func(context.Context) error { order = append(order, "stop2"); return nil },
	})

	require.NoError(t, lc.Start(context.Background()))
	require.NoError(t, lc.Stop(context.Background()))
	assert.Equal(t, []string{"start1", "start2", "stop2", "stop1"}, order)
}

func TestLifecycle_PreStartRunsFirst(t *testing.T) {
	lc := di.NewLifecycle()
	var order []string
	lc.Append(di.Hook{OnStart: func(context.Context) error { order = append(order, "main"); return nil }})
	lc.AppendPreStart(di.Hook{OnStart: func(context.Context) error { order = append(order, "pre"); return nil }})

	require.NoError(t, lc.Start(context.Background()))
	assert.Equal(t, []string{"pre", "main"}, order)
}

func TestLifecycle_PreStartFailureAborts(t *testing.T) {
	lc := di.NewLifecycle()
	started := false
	lc.AppendPreStart(di.Hook{OnStart: func(context.Context) error { return errors.New("pre boom") }})
	lc.Append(di.Hook{OnStart: func(context.Context) error { started = true; return nil }})

	require.Error(t, lc.Start(context.Background()))
	assert.False(t, started, "main hooks must not run when PreStart fails")
}

func TestLifecycle_StopAggregatesErrors(t *testing.T) {
	lc := di.NewLifecycle()
	lc.Append(di.Hook{OnStop: func(context.Context) error { return errors.New("first") }})
	lc.Append(di.Hook{OnStop: func(context.Context) error { return errors.New("second") }})

	err := lc.Stop(context.Background())
	require.Error(t, err)
}

func TestLifecycle_NilSafe(t *testing.T) {
	var lc *di.Lifecycle
	lc.Append(di.Hook{})
	require.NoError(t, lc.Start(context.Background()))
	require.NoError(t, lc.Stop(context.Background()))
}

// ---- InvokeOption ----

func TestInvokeOption_RunsOnce(t *testing.T) {
	var runs int
	root := di.New(di.InvokeOption(func(di.Injector) error { runs++; return nil }))
	defer di.Shutdown(root)

	lc := di.MustInvoke[*di.Lifecycle](root)
	require.NoError(t, lc.Start(context.Background()))
	assert.Equal(t, 1, runs)
}

func TestInvokeOption_InjectsDeps(t *testing.T) {
	var got string
	root := di.New(
		func(i di.Injector) { di.ProvideValue(i, &Dep{V: "wired"}) },
		di.InvokeOption(func(i di.Injector) error {
			got = di.MustInvoke[*Dep](i).V
			return nil
		}),
	)
	defer di.Shutdown(root)

	require.NoError(t, di.MustInvoke[*di.Lifecycle](root).Start(context.Background()))
	assert.Equal(t, "wired", got)
}

func TestInvokeOption_ErrorAbortsStart(t *testing.T) {
	boom := errors.New("start boom")
	root := di.New(di.InvokeOption(func(di.Injector) error { return boom }))
	defer di.Shutdown(root)

	require.ErrorIs(t, di.MustInvoke[*di.Lifecycle](root).Start(context.Background()), boom)
}

// ---- Describe / 诊断 ----

func TestDescribe_ListsServicesAndGroups(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)
	di.ProvideValue(root, &Dep{V: "x"})
	di.ProvideGroupItem(root, "g1")

	services, groups := di.Describe(root)
	assert.NotEmpty(t, services)
	require.NotEmpty(t, groups)
	assert.Equal(t, "string", groups[0].ElemType)
	assert.Equal(t, 1, groups[0].Count)
}

func TestAssertGroupsNonEmpty(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)
	di.ProvideGroupItem(root, "x")

	require.NoError(t, di.AssertGroupsNonEmpty(root, di.GroupOf[string]()))
	assert.Error(t, di.AssertGroupsNonEmpty(root, di.GroupOf[int]()))
}

// ---- 严格装配 ----

func TestStrictWiring_GetGroupDuringAssemblyPanics(t *testing.T) {
	di.SetStrictWiring(true)
	t.Cleanup(func() { di.SetStrictWiring(false) })

	assert.Panics(t, func() {
		di.New(func(i di.Injector) { _ = di.GetGroup[string](i) })
	})
}

func TestStrictWiring_AllowsReadAfterAssembly(t *testing.T) {
	di.SetStrictWiring(true)
	t.Cleanup(func() { di.SetStrictWiring(false) })

	root := di.New(func(i di.Injector) { di.ProvideGroupItem(i, "x") })
	defer di.Shutdown(root)

	assert.NotPanics(t, func() { _ = di.GetGroup[string](root) })
}

// ---- Module / Options / Named / Lazy ----

func TestModuleAndOptions(t *testing.T) {
	root := di.New(
		di.Options(
			di.Module("a", func(i di.Injector) { di.ProvideValue(i, &Dep{V: "mod"}) }),
			di.Module("b", nil),
		),
	)
	defer di.Shutdown(root)

	assert.Equal(t, "mod", di.MustInvoke[*Dep](root).V)
}

func TestProvideNamed(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)

	di.ProvideNamed(root, "primary", func(di.Injector) (*Dep, error) {
		return &Dep{V: "named"}, nil
	})
	v, err := do.InvokeNamed[*Dep](root, "primary")
	require.NoError(t, err)
	assert.Equal(t, "named", v.V)
}

func TestProvideLazy_RunsAtPreStart(t *testing.T) {
	root := di.New(func(i di.Injector) {
		di.ProvideLazy[string](i, func(di.Injector) string { return "lazy" })
	})
	defer di.Shutdown(root)

	assert.Empty(t, di.GetGroup[string](root), "lazy item must not exist before PreStart")

	require.NoError(t, di.MustInvoke[*di.Lifecycle](root).StartPreStartOnly(context.Background()))
	assert.Equal(t, []string{"lazy"}, di.GetGroup[string](root))
}

func TestProvideLazyE_PropagatesError(t *testing.T) {
	boom := errors.New("lazy boom")
	root := di.New(func(i di.Injector) {
		di.ProvideLazyE[string](i, func(di.Injector) (string, error) { return "", boom })
	})
	defer di.Shutdown(root)

	require.ErrorIs(t, di.MustInvoke[*di.Lifecycle](root).StartPreStartOnly(context.Background()), boom)
}

func TestReport_IncludesServicesAndGroups(t *testing.T) {
	root := di.New()
	defer di.Shutdown(root)
	di.ProvideValue(root, &Dep{V: "x"})
	di.ProvideGroupItem(root, "g")

	out := di.Report(root)
	assert.Contains(t, out, "Dep")
	assert.Contains(t, out, "string")
}

func TestLifecycle_RequireStartStop(t *testing.T) {
	lc := di.NewLifecycle()
	assert.NotPanics(t, lc.RequireStart)
	assert.NotPanics(t, lc.RequireStop)
}
