package cli

import (
	"testing"

	"go.uber.org/fx"
)

func TestBuildMCPCmd_Creation(t *testing.T) {
	cmd := buildMCPCmd([]fx.Option{})
	if cmd == nil || cmd.Use != "mcp" {
		t.Fatalf("unexpected mcp command: %+v", cmd)
	}
}
