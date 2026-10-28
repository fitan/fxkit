package cli

import (
	"testing"

	"github.com/fitan/fxkit/di"
)

func TestBuildMCPCmd_Creation(t *testing.T) {
	cmd := buildMCPCmd([]di.Option{})
	if cmd == nil || cmd.Use != "mcp" {
		t.Fatalf("unexpected mcp command: %+v", cmd)
	}
}
