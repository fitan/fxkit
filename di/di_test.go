package di_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fitan/fxkit/di"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MyService struct {
	Name string
}

func TestDI_Basic(t *testing.T) {
	opt := func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*MyService, error) {
			return &MyService{Name: "test-service"}, nil
		})
	}

	root := di.New(opt)
	defer root.Shutdown()

	svc, err := di.Invoke[*MyService](root)
	require.NoError(t, err)
	assert.Equal(t, "test-service", svc.Name)
}

func TestDI_Lifecycle(t *testing.T) {
	var started, stopped bool

	opt := func(i di.Injector) {
		lc := di.MustInvoke[*di.Lifecycle](i)
		lc.Append(di.Hook{
			OnStart: func(ctx context.Context) error {
				started = true
				return nil
			},
			OnStop: func(ctx context.Context) error {
				stopped = true
				return nil
			},
		})
	}

	root := di.New(opt)
	lc := di.MustInvoke[*di.Lifecycle](root)

	ctx := context.Background()
	require.NoError(t, lc.Start(ctx))
	assert.True(t, started)

	require.NoError(t, lc.Stop(ctx))
	assert.True(t, stopped)
}

func TestDI_Lifecycle_Rollback(t *testing.T) {
	var s1Started, s1Stopped bool

	opt := func(i di.Injector) {
		lc := di.MustInvoke[*di.Lifecycle](i)
		lc.Append(di.Hook{
			OnStart: func(ctx context.Context) error {
				s1Started = true
				return nil
			},
			OnStop: func(ctx context.Context) error {
				s1Stopped = true
				return nil
			},
		})
		lc.Append(di.Hook{
			OnStart: func(ctx context.Context) error {
				return errors.New("start failed")
			},
		})
	}

	root := di.New(opt)
	lc := di.MustInvoke[*di.Lifecycle](root)

	ctx := context.Background()
	err := lc.Start(ctx)
	require.Error(t, err)
	assert.True(t, s1Started)
	assert.True(t, s1Stopped, "s1 should be rolled back")
}

func TestDI_Group(t *testing.T) {
	opt1 := func(i di.Injector) {
		di.ProvideGroupItem(i, "item-1")
	}
	opt2 := func(i di.Injector) {
		di.ProvideGroupItem(i, "item-2")
	}

	root := di.New(opt1, opt2)
	items := di.GetGroup[string](root)
	assert.ElementsMatch(t, []string{"item-1", "item-2"}, items)
}
