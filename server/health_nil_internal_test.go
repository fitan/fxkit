package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fitan/fxkit/di"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type internalChecker struct{ name string }

func (c internalChecker) Name() string              { return c.name }
func (internalChecker) Check(context.Context) error { return nil }

type pointerChecker struct{}

func (*pointerChecker) Name() string                { return "ptr" }
func (*pointerChecker) Check(context.Context) error { return nil }

// TestRegisterHealthRoutes_SkipAndServe 覆盖显式跳过：ok=false 不注册，/readyz 只含真实检查项。
func TestRegisterHealthRoutes_SkipAndServe(t *testing.T) {
	root := di.New(
		ProvideHealthChecker(internalChecker{name: "db"}),
		ProvideHealthCheckerLazy(func(di.Injector) (HealthChecker, bool, error) {
			return nil, false, nil
		}),
	)
	defer root.Shutdown()
	require.NoError(t, di.MustInvoke[*di.Lifecycle](root).StartPreStartOnly(context.Background()))

	mux := chi.NewRouter()
	require.NoError(t, registerHealthRoutes(root, mux))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"db":"ok"`)
	require.NotContains(t, rec.Body.String(), "null")
}

// TestProvideHealthCheckerLazy_NilWithOKFailsStartup 回归：ok=true 却返回 nil 是编程错误。
func TestProvideHealthCheckerLazy_NilWithOKFailsStartup(t *testing.T) {
	root := di.New(ProvideHealthCheckerLazy(func(di.Injector) (HealthChecker, bool, error) {
		return nil, true, nil
	}))
	defer root.Shutdown()

	err := di.MustInvoke[*di.Lifecycle](root).StartPreStartOnly(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil checker")
}

// TestProvideHealthCheckerLazy_TypedNilFailsStartup 覆盖 typed-nil（接口非 nil，但底层指针为 nil）。
func TestProvideHealthCheckerLazy_TypedNilFailsStartup(t *testing.T) {
	var pc *pointerChecker
	root := di.New(ProvideHealthCheckerLazy(func(di.Injector) (HealthChecker, bool, error) {
		return pc, true, nil
	}))
	defer root.Shutdown()

	err := di.MustInvoke[*di.Lifecycle](root).StartPreStartOnly(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil checker")
}

// TestProvideHealthChecker_PanicsOnNil 普通注册不接受 nil（装配期错误）。
func TestProvideHealthChecker_PanicsOnNil(t *testing.T) {
	assert.Panics(t, func() {
		_ = di.New(ProvideHealthChecker(nil))
	})
}

// TestRegisterHealthRoutes_RejectsNilInjectedDirectly 即使绕过 Provide* 直接写 group，也不会 panic 在 sort 上。
func TestRegisterHealthRoutes_RejectsNilInjectedDirectly(t *testing.T) {
	root := di.New(func(i di.Injector) {
		di.ProvideGroupItem[HealthChecker](i, nil)
	})
	defer root.Shutdown()
	require.NoError(t, di.MustInvoke[*di.Lifecycle](root).StartPreStartOnly(context.Background()))

	err := registerHealthRoutes(root, chi.NewRouter())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is nil")
}

// TestRegisterHealth_RejectsNilChecker 直接调用 RegisterHealth 也必须拒绝 nil。
func TestRegisterHealth_RejectsNilChecker(t *testing.T) {
	err := RegisterHealth(HealthParams{Mux: chi.NewRouter(), Checkers: []HealthChecker{nil}})
	require.Error(t, err)
}
