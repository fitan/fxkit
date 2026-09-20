package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/fx"
)

func TestBuildServeCmd_ReturnsErrorOnStartFailure(t *testing.T) {
	errBoom := errors.New("boom during start")
	badModule := fx.Invoke(func(lc fx.Lifecycle) {
		lc.Append(fx.Hook{
			OnStart: func(context.Context) error {
				return errBoom
			},
		})
	})

	cmd := buildServeCmd([]fx.Option{badModule})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom during start") {
		t.Fatalf("expected error containing 'boom during start', got %v", err)
	}
}
