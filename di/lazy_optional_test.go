package di

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvideLazyOptional_WritesOnlyWhenOK(t *testing.T) {
	root := New(func(i Injector) {
		ProvideLazyOptionalE[string](i, func(Injector) (string, bool, error) {
			return "skipped", false, nil
		})
		ProvideLazyOptionalE[string](i, func(Injector) (string, bool, error) {
			return "kept", true, nil
		})
	})
	defer root.Shutdown()

	require.NoError(t, MustInvoke[*Lifecycle](root).StartPreStartOnly(context.Background()))
	assert.Equal(t, []string{"kept"}, GetGroup[string](root))
}

func TestProvideLazyOptional_PropagatesError(t *testing.T) {
	root := New(func(i Injector) {
		ProvideLazyOptionalE[string](i, func(Injector) (string, bool, error) {
			return "", false, errors.New("boom")
		})
	})
	defer root.Shutdown()

	err := MustInvoke[*Lifecycle](root).StartPreStartOnly(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}
