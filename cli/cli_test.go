package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fitan/fxkit/di"
)

func TestBuildServeCmd_ReturnsErrorOnStartFailure(t *testing.T) {
	errBoom := errors.New("boom during start")
	badModule := func(i di.Injector) {
		lc, _ := di.Invoke[*di.Lifecycle](i)
		if lc != nil {
			lc.Append(di.Hook{
				OnStart: func(context.Context) error {
					return errBoom
				},
			})
		}
	}

	cmd := buildServeCmd([]di.Option{badModule})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom during start") {
		t.Fatalf("expected error containing 'boom during start', got %v", err)
	}
}
